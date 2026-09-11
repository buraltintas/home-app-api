// Package textnorm holds the two string foldings this product compares Turkish names
// with. They live here rather than inside search because the catalogue, the location
// index and the import matcher all have to fold a name exactly the way search does --
// a second implementation that disagreed by one character would make "Kadıköy" and
// "KADIKOY" two different places in one part of the system and the same place in another.
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize lowercases and trims, leaving the letters themselves intact. It lowercases by
// the Unicode default rather than by Turkish rules, so "I" becomes a dotted "i" and not
// "ı"; Turkish "İ" becomes an "i" carrying a combining dot, which Fold then removes.
// Nothing should compare its output to a Turkish word without folding first -- Key does
// both, and is what almost every caller wants.
func Normalize(raw string) string {
	return norm.NFC.String(strings.ToLower(strings.TrimSpace(raw)))
}

// Fold strips diacritics so that a name typed without them still matches. Dotless "ı" is
// replaced before decomposition because it carries no mark to strip.
func Fold(raw string) string {
	raw = strings.NewReplacer("ı", "i", "ß", "ss").Replace(raw)
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, norm.NFD.String(raw))
}

// Key is what a name is stored and searched by: folded, lowercased, and reduced to
// letters, digits and single spaces. Punctuation in a place name is decoration -- nobody
// types "Kadıköy/İstanbul" into a search field the way it is printed on a form.
func Key(raw string) string {
	folded := Fold(Normalize(raw))
	var b strings.Builder
	b.Grow(len(folded))
	space := false
	for _, r := range folded {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}
