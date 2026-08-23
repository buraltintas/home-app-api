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
	// The same query has to produce the same intent. At the default temperature it did
	// not: "Salon için büyük bir ayna" came back as decoration, then decoration+furniture,
	// then decoration+home_accessories+furniture on three identical requests -- which is
	// why the same search returned different stores each time. This is a classification
	// task with one right answer, so there is nothing for sampling to contribute.
	r, e := p.client.Responses.New(ctx, responses.ResponseNewParams{Model: p.model, Temperature: openai.Float(0), Input: responses.ResponseNewParamsInputUnion{OfString: openai.String(prompt)}, Text: responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigParamOfJSONSchema("search_intent", p.schema)}})
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
	return fmt.Sprintf(`Parse and classify this physical-store search. The input may be Turkish, English, German, or Russian. Set scope to home_living when the user wants to discover or buy home/living products or stores, including indirect needs such as dowry shopping, refreshing a room, bedding, curtains, furniture, lighting, decoration, kitchenware, bathroom products, carpets, storage, tableware, and household accessories. A query may consist only of a home/living store or brand name (for example IKEA or Madame Coco); classify it as home_living and copy the full store or brand name into store_name without translating or shortening it. Also extract store_name when it appears alongside products or a location. Use at most three categories, and only the ones the query clearly implies; for a bare store or brand name leave categories empty instead of listing everything that store might sell. Set scope to out_of_scope for an explicit unrelated product, service, or business such as a tire shop, restaurant, pharmacy, or hairdresser. Set scope to unclear for greetings, chitchat, nonsense, or an intent that cannot be confidently understood. For out_of_scope or unclear, leave store_name, categories, product_terms, style_terms, price_intent, attributes, and semantic_terms empty. Detect query_language as tr, en, de, or ru. Preserve proper nouns and the original query exactly, and map meaning into one canonical schema regardless of language. Categories must use canonical slugs; product_terms, style_terms, and semantic_terms must use concise stable English-like concept keys, never translated display copy. Do not invent coordinates. Requested response locale=%s. Original query=%q`, c.Locale, query)
}
