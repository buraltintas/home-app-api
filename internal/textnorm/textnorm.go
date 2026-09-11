// Package textnorm holds the two string foldings this product compares Turkish names
// with. They live here rather than inside search because the catalogue, the location
// index and the import matcher all have to fold a name exactly the way search does --
// a second implementation that disagreed by one character would make "Kadıköy" and
// "KADIKOY" two different places in one part of the system and the same place in another.
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
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

var (
	turkishTitle = cases.Title(language.Turkish)
	plainTitle   = cases.Title(language.English)
)

// turkishLetters are the letters whose casing rules differ from everybody else's. A name
// containing one of them is written in Turkish; a name containing none of them may not be.
const turkishLetters = "çÇğĞıİöÖşŞüÜ"

// TitlePlace cases a place name. Every name in the administrative dataset is Turkish by
// construction, so Turkish rules apply unconditionally: "ADIYAMAN" is "Adıyaman", and the
// ordinary rules would give "Adiyaman", which is a different word. Nothing here is ever an
// acronym -- "BOLU" and "KARS" are provinces.
func TitlePlace(raw string) string {
	return turkishTitle.String(strings.TrimSpace(raw))
}

// Title cases a shop's sign, where the language is not known in advance.
//
// A Turkish catalogue is full of English words: chains publish branch codes like
// "ANK ACITY AVM" beside names like "BALIKESİR MAĞAZASI". Turkish rules give the second one
// right and turn the first into "Acıty"; the ordinary rules swap the mistakes over. So the
// name is read first: if any letter in it is one only Turkish uses, the whole name is cased
// as Turkish, and otherwise as plain Latin.
//
// Word by word would seem better and is worse, which is why this was measured rather than
// argued. Across the shouted names in the live catalogue, 49 words are ones the two rules
// disagree about -- words holding an ASCII "I" and no Turkish letter. Forty-three of them
// are Turkish written without its diacritics (HALI, TASARIM, YAPI, KONYAALTI, AYDINLATMA)
// and want the dotless ı; six are English (COLLECTION, BOUTIQUE, SIEMENS) and do not.
// Casing the whole name by the language it announces gets the forty-three right. The six
// keep a wrong letter, and that is the better trade, not an oversight.
//
// Two things are left exactly as they are. A name that is not shouting was styled by its
// owner -- "English Home" is not ours to restyle. And a short all-capital name is an
// acronym: IKEA is not Ikea.
func Title(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" || name != strings.ToUpper(name) {
		return name
	}
	if len([]rune(name)) <= 4 && !strings.ContainsRune(name, ' ') {
		return name
	}
	caser := plainTitle
	if strings.ContainsAny(name, turkishLetters) {
		caser = turkishTitle
	}
	words := strings.Split(name, " ")
	for i, word := range words {
		// A short shouted word inside a name is an abbreviation the writer meant: AVM is a
		// shopping centre, CAD is a street. Calming them to "Avm" and "Cad" makes the name
		// read like a mistake.
		if len([]rune(word)) <= 3 {
			continue
		}
		words[i] = caser.String(word)
	}
	return strings.Join(words, " ")
}

// Compact is the form two names are compared in: Key with the spaces taken out, so
// "ENGLISH HOME KADIKÖY AVM" and "English Home Kadıköy AVM" are one string. It is what the
// catalogue stores in compact_name and what a search has to fold a query into before it can
// meet that column -- the two must be the same function or the column silently stops
// matching anything.
func Compact(name string) string {
	return strings.ReplaceAll(Key(name), " ", "")
}
