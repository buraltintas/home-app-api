package i18n

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

type Locale string

const (
	LocaleTR Locale = "tr"
	LocaleEN Locale = "en"
	LocaleDE Locale = "de"
	LocaleRU Locale = "ru"
)

const DefaultLocale = LocaleTR

var supported = []Locale{LocaleTR, LocaleEN, LocaleDE, LocaleRU}

func Supported() []Locale { return append([]Locale(nil), supported...) }

func IsSupported(locale Locale) bool {
	switch locale {
	case LocaleTR, LocaleEN, LocaleDE, LocaleRU:
		return true
	default:
		return false
	}
}

func Normalize(raw string) (Locale, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexAny(raw, "-_"); i >= 0 {
		raw = raw[:i]
	}
	locale := Locale(raw)
	return locale, IsSupported(locale)
}

type weightedLocale struct {
	locale Locale
	q      float64
	order  int
}

func FromAcceptLanguage(header string) (Locale, bool) {
	var candidates []weightedLocale
	for order, part := range strings.Split(header, ",") {
		bits := strings.Split(strings.TrimSpace(part), ";")
		locale, ok := Normalize(bits[0])
		if !ok {
			continue
		}
		q := 1.0
		for _, parameter := range bits[1:] {
			parameter = strings.TrimSpace(parameter)
			if strings.HasPrefix(parameter, "q=") {
				if parsed, err := strconv.ParseFloat(strings.TrimPrefix(parameter, "q="), 64); err == nil {
					q = parsed
				}
			}
		}
		if q > 0 {
			candidates = append(candidates, weightedLocale{locale: locale, q: q, order: order})
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].q == candidates[j].q {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].q > candidates[j].q
	})
	return candidates[0].locale, true
}

type localeContextKey struct{}
type explicitContextKey struct{}

func WithLocale(ctx context.Context, locale Locale) context.Context {
	if !IsSupported(locale) {
		locale = DefaultLocale
	}
	return context.WithValue(ctx, localeContextKey{}, locale)
}

func FromContext(ctx context.Context) Locale {
	if locale, ok := ctx.Value(localeContextKey{}).(Locale); ok && IsSupported(locale) {
		return locale
	}
	return DefaultLocale
}

func WithExplicitLocale(ctx context.Context, locale Locale) context.Context {
	return context.WithValue(WithLocale(ctx, locale), explicitContextKey{}, true)
}

func HasExplicitLocale(ctx context.Context) bool {
	explicit, _ := ctx.Value(explicitContextKey{}).(bool)
	return explicit
}

func ResolveRequest(explicit, acceptLanguage string, fallback Locale) (Locale, bool) {
	if locale, ok := Normalize(explicit); ok {
		return locale, true
	}
	if locale, ok := FromAcceptLanguage(acceptLanguage); ok {
		return locale, false
	}
	if !IsSupported(fallback) {
		fallback = DefaultLocale
	}
	return fallback, false
}
