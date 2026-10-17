package dates

import "testing"

func TestParseLoose(t *testing.T) {
	ok := map[string]Partial{
		"":                {},
		"1980-03-14":      {1980 * 0, 0, 0},
		"3/14/1980":       {3, 14, 1980},
		"03/04/1980":      {3, 4, 1980},
		"3/14":            {3, 14, 0},
		"March 14":        {3, 14, 0},
		"mar 14, 1980":    {3, 14, 1980},
		"March 14th 1980": {3, 14, 1980},
		"1980":            {0, 0, 1980},
		"2/29":            {2, 29, 0},
	}
	ok["1980-03-14"] = Partial{3, 14, 1980}
	for in, want := range ok {
		got, err := ParseLoose(in)
		if err != nil || got != want {
			t.Errorf("ParseLoose(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"13/40/1980", "2/30", "Smarch 3", "yesterday", "1700", "0/5", "2/29/1981", "80"} {
		if _, err := ParseLoose(in); err == nil {
			t.Errorf("ParseLoose(%q) should fail", in)
		}
	}
}
