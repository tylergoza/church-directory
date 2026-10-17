package dates

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var (
	isoRe    = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})$`)
	usRe     = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	usShort  = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})$`)
	yearRe   = regexp.MustCompile(`^(\d{4})$`)
	wordsRe  = regexp.MustCompile(`^([A-Za-z]+)\.?\s+(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?$`)
	monthMap = func() map[string]int {
		m := map[string]int{"sept": 9}
		for i := 1; i <= 12; i++ {
			n := strings.ToLower(monthNames[i])
			m[n] = i
			m[n[:3]] = i
		}
		return m
	}()
)

// ParseLoose reads a date typed or exported by hand: 1980-03-14,
// 3/14/1980, 3/14, "March 14", "Mar 14, 1980" or just a year. Slash dates
// are month first. Blank is an empty date. The result is validated.
func ParseLoose(s string) (Partial, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Partial{}, nil
	}
	var p Partial
	num := func(v string) int { n, _ := strconv.Atoi(v); return n }
	switch {
	case isoRe.MatchString(s):
		m := isoRe.FindStringSubmatch(s)
		p = Partial{Year: num(m[1]), Month: num(m[2]), Day: num(m[3])}
	case usRe.MatchString(s):
		m := usRe.FindStringSubmatch(s)
		p = Partial{Month: num(m[1]), Day: num(m[2]), Year: num(m[3])}
	case usShort.MatchString(s):
		m := usShort.FindStringSubmatch(s)
		p = Partial{Month: num(m[1]), Day: num(m[2])}
	case yearRe.MatchString(s):
		p = Partial{Year: num(s)}
	case wordsRe.MatchString(s):
		m := wordsRe.FindStringSubmatch(s)
		mon, ok := monthMap[strings.ToLower(m[1])]
		if !ok {
			return Partial{}, errors.New("\"" + s + "\" isn't a date I understand")
		}
		p = Partial{Month: mon, Day: num(m[2]), Year: num(m[3])}
	default:
		return Partial{}, errors.New("\"" + s + "\" isn't a date I understand")
	}
	if p.Month == 0 && p.Day != 0 || p.Month == 0 && p.Year == 0 {
		return Partial{}, errors.New("\"" + s + "\" isn't a date I understand")
	}
	if err := p.Validate(); err != nil {
		return Partial{}, err
	}
	return p, nil
}
