package search

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/burakaltintas/home-app-api/internal/observability"
	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

type OpenAIParser struct {
	client  openai.Client
	model   string
	timeout time.Duration
	schema  map[string]any
}

func NewOpenAIParser(key, model string, timeout time.Duration) *OpenAIParser {
	r := jsonschema.Reflector{AllowAdditionalProperties: false, DoNotReference: true}
	raw, _ := json.Marshal(r.Reflect(Intent{}))
	var schema map[string]any
	_ = json.Unmarshal(raw, &schema)
	return &OpenAIParser{openai.NewClient(option.WithAPIKey(key)), model, timeout, schema}
}
func (p *OpenAIParser) ParseSearchIntent(ctx context.Context, query string, c Context) (Intent, error) {
	started := time.Now()
	ctx, finish := observability.StartSpan(ctx, "provider.openai.parse_intent")
	out, err := p.parseSearchIntent(ctx, query, c)
	finish(err)
	observability.Provider("openai", observability.Outcome(err), time.Since(started))
	return out, err
}

func (p *OpenAIParser) parseSearchIntent(ctx context.Context, query string, c Context) (Intent, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	prompt := fmt.Sprintf("Parse this Turkish home/living physical-store search into the schema. Use only canonical category slugs. Do not invent coordinates. Locale=%s. Query=%q", c.Locale, query)
	r, e := p.client.Responses.New(ctx, responses.ResponseNewParams{Model: p.model, Input: responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)}, Text: responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigParamOfJSONSchema("search_intent", p.schema)}})
	if e != nil {
		return Intent{}, e
	}
	var out Intent
	if e = json.Unmarshal([]byte(r.OutputText()), &out); e != nil {
		return out, e
	}
	if e = Validate(out); e != nil {
		return Intent{}, e
	}
	return out, nil
}
