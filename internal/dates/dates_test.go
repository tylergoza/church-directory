package dates

import (
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	next := time.Now().Year() + 1
	cases := []struct {
		name string
		p    Partial
		ok   bool
	}{
		{"empty", Partial{}, true},
		{"year only", Partial{Year: 1985}, true},
		{"month only", Partial{Month: 3}, true},
		{"month and year", Partial{Month: 3, Year: 1985}, true},
		{"full", Partial{3, 14, 1985}, true},
		{"day needs month", Partial{Day: 14}, false},
		{"day needs month with year", Partial{Day: 14, Year: 1985}, false},
		{"month 13", Partial{Month: 13}, false},
		{"negative month", Partial{Month: -1}, false},
		{"day 32", Partial{Month: 1, Day: 32}, false},
		{"april 31", Partial{Month: 4, Day: 31}, false},
		{"jan 31", Partial{Month: 1, Day: 31}, true},
		{"feb 30", Partial{Month: 2, Day: 30}, false},
		{"feb 29 no year", Partial{Month: 2, Day: 29}, true},
		{"feb 29 leap year", Partial{2, 29, 1984}, true},
		{"feb 29 year 2000", Partial{2, 29, 2000}, true},
		{"feb 29 not leap", Partial{2, 29, 1985}, false},
		{"feb 29 1900", Partial{2, 29, 1900}, false},
		{"feb 28 not leap", Partial{2, 28, 1985}, true},
		{"year too early", Partial{Year: 1700}, false},
		{"year in the future", Partial{Year: next}, false},
		{"this year", Partial{Year: time.Now().Year()}, true},
	}
	for _, c := range cases {
		if err := c.p.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate(%+v) = %v, want ok=%v", c.name, c.p, err, c.ok)
		}
	}
}

func TestFormat(t *testing.T) {
	full := Partial{3, 14, 1985}
	cases := []struct {
		name string
		p    Partial
		show Shown
		want string
	}{
		{"all", full, All, "March 14, 1985"},
		{"month and day", full, Shown{Month: true, Day: true}, "March 14"},
		{"month only", full, Shown{Month: true}, "March"},
		{"month and year", full, Shown{Month: true, Year: true}, "March 1985"},
		{"year only", full, Shown{Year: true}, "1985"},
		{"day without month shown", full, Shown{Day: true}, ""},
		{"day and year without month", full, Shown{Day: true, Year: true}, "1985"},
		{"nothing shown", full, Shown{}, ""},
		{"empty date", Partial{}, All, ""},
		{"no year", Partial{Month: 12, Day: 25}, All, "December 25"},
		{"month only entered", Partial{Month: 5}, All, "May"},
		{"year only entered", Partial{Year: 1990}, All, "1990"},
		{"month and year entered", Partial{Month: 5, Year: 1990}, All, "May 1990"},
		{"bad month ignored", Partial{Month: 13, Day: 1}, All, ""},
	}
	for _, c := range cases {
		if got := c.p.Format(c.show); got != c.want {
			t.Errorf("%s: Format = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	p, err := Parse(" 3", "14 ", "1985")
	if err != nil || p != (Partial{3, 14, 1985}) {
		t.Errorf("Parse = %+v, %v", p, err)
	}
	if p, err := Parse("", "", ""); err != nil || !p.IsZero() {
		t.Errorf("blank Parse = %+v, %v", p, err)
	}
	for _, bad := range [][3]string{{"x", "", ""}, {"", "1.5", ""}, {"", "", "abc"}} {
		if _, err := Parse(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("Parse(%v) should fail", bad)
		}
	}
}
