package search

import (
	"strings"
	"testing"

	"github.com/burakaltintas/home-app-api/internal/i18n"
)

func TestOpenAIIntentPromptPreservesUnicodeAndCanonicalContract(t *testing.T) {
	query := "где купить шторы в Анталии"
	prompt := intentPrompt(query, Context{Locale: i18n.LocaleRU})
	for _, required := range []string{query, "Turkish, English, German, or Russian", "query_language", "canonical slugs", "scope to home_living", "scope to out_of_scope", "scope to unclear", "store_name", "only of a home/living store", "locale=ru"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt missing %q: %s", required, prompt)
		}
	}
}
