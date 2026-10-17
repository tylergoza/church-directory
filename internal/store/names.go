package store

import (
	"strings"
	"unicode"
)

// DisplayName is how a person's name shows: their goes-by in place of the
// first name when they have one ("Ty Goza"), then the last name and the
// suffix ("Tyler Goza Jr."). The legal first name is on the person's page
// (LegalName).
func DisplayName(first, last, goesBy, suffix string) string {
	given := strings.TrimSpace(goesBy)
	if given == "" {
		given = strings.TrimSpace(first)
	}
	return joinName(given, last, suffix)
}

// LegalName is the name with the first name, even when they go by another.
func LegalName(first, last, suffix string) string { return joinName(first, last, suffix) }

func joinName(given, last, suffix string) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{given, last, suffix} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// foldGroups maps accented and special Latin letters (Latin-1 and Latin
// Extended-A, lower case) to a plain spelling: the first rune of each group
// is the replacement, the rest are folded to it. Hand-written because the
// standard library has no Unicode normalization.
var foldGroups = []string{
	"aàáâãäåāăąǎ", "cçćĉċč", "dďđ", "eèéêëēĕėęěȩ", "gĝğġģ", "hĥħ",
	"iìíîïĩīĭįıǐ", "jĵ", "kķ", "lĺļľŀł", "nñńņňŉ", "oòóôõöōŏőǒ", "rŕŗř",
	"sśŝşš", "tţťŧ", "uùúûüũūŭůűųǔ", "wŵ", "yýÿŷ", "zźżž",
}

var foldMap = func() map[rune]string {
	m := map[rune]string{'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'þ': "th", 'ð': "d", 'ĳ': "ij"}
	for _, g := range foldGroups {
		r := []rune(g)
		for _, c := range r[1:] {
			m[c] = string(r[0])
		}
	}
	return m
}()

// Fold lower-cases s, takes the accents off Latin letters and collapses
// runs of spaces and punctuation that don't matter when comparing names.
// "  José   O'Neil-Smith " and "jose o'neil-smith" fold the same. Dots are
// dropped, so "Jr." and "Jr" match, and so are apostrophes ("O'Neil" and
// "ONeil" are the same name). Hyphens and commas count as spaces.
func Fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		r = unicode.ToLower(r)
		switch {
		case r == '.' || r == '\'' || r == '’' || r == '`':
			continue
		case unicode.IsSpace(r) || r == '-' || r == '–' || r == '—' || r == ',':
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		if f, ok := foldMap[r]; ok {
			b.WriteString(f)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NameKey is the person's name boiled down for matching: folded last name,
// folded suffix, and the folded first name and goes-by (either may be
// empty). It is the one place the name-match rule lives; NameMatches and
// anything else that asks "is this the same name?" use it.
type NameKey struct {
	Last, Suffix string
	Given        []string // folded first name and goes-by, no blanks, no repeats
}

// NewNameKey folds a name for matching.
func NewNameKey(first, last, goesBy, suffix string) NameKey {
	k := NameKey{Last: Fold(last), Suffix: Fold(suffix)}
	for _, g := range []string{Fold(first), Fold(goesBy)} {
		if g == "" {
			continue
		}
		if len(k.Given) == 1 && k.Given[0] == g {
			continue
		}
		k.Given = append(k.Given, g)
	}
	return k
}

// Matches is the match rule: the same last name, the same suffix (no
// suffix only matches no suffix), and a first name or goes-by of one the
// same as a first name or goes-by of the other.
func (k NameKey) Matches(o NameKey) bool {
	if k.Last == "" || k.Last != o.Last || k.Suffix != o.Suffix {
		return false
	}
	for _, a := range k.Given {
		for _, b := range o.Given {
			if a == b {
				return true
			}
		}
	}
	return false
}
