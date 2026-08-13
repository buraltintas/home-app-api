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
	prompt := intentPrompt(query, c)
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

func intentPrompt(query string, c Context) string {
	return fmt.Sprintf(`Parse this home/living physical-store search. The input may be Turkish, English, German, or Russian. Detect query_language as tr, en, de, or ru. Preserve proper nouns and the original query exactly, and map meaning into one canonical schema regardless of language. Categories must use canonical slugs; product_terms and style_terms must use stable English-like concept keys, never translated display copy. Do not invent coordinates. Requested response locale=%s. Original query=%q`, c.Locale, query)
}
