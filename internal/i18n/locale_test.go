package i18n

import "testing"

func TestLocaleNormalizationAndFallbackInputs(t *testing.T) {
	tests := map[string]Locale{"tr": LocaleTR, "tr-TR": LocaleTR, "en": LocaleEN, "en-US": LocaleEN, "en-GB": LocaleEN, "de": LocaleDE, "de-DE": LocaleDE, "ru": LocaleRU, "ru-RU": LocaleRU}
	for raw, want := range tests {
		got, ok := Normalize(raw)
		if !ok || got != want {
			t.Fatalf("Normalize(%q)=%q,%v want=%q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"fr", "es", "ar", "", "---"} {
		if got, ok := Normalize(raw); ok {
			t.Fatalf("Normalize(%q) unexpectedly supported as %q", raw, got)
		}
	}
	if got, ok := FromAcceptLanguage("fr-FR, de-DE;q=0.9, en;q=0.8"); !ok || got != LocaleDE {
		t.Fatalf("Accept-Language resolved to %q,%v", got, ok)
	}
	if _, ok := FromAcceptLanguage("garbage;q=x, fr;q=1"); ok {
		t.Fatal("malformed/unsupported Accept-Language resolved")
	}
}

func TestMessageCatalogsHaveIdenticalCompleteKeySets(t *testing.T) {
	base := Keys(DefaultLocale)
	if len(base) == 0 {
		t.Fatal("default catalog is empty")
	}
	for _, locale := range Supported() {
		catalog := Keys(locale)
		if len(catalog) != len(base) {
			t.Fatalf("%s has %d messages; default has %d", locale, len(catalog), len(base))
		}
		for key := range base {
			if catalog[key] == "" {
				t.Fatalf("%s is missing %s", locale, key)
			}
		}
	}
}
