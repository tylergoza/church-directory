package store

import (
	"time"

	"github.com/tylergoza/church-directory/internal/dates"
)

// Under13 is the age under which a person may not have their own login.
const Under13 = 13

// AgeOn is the person's age in whole years on the given day, and whether it
// is known at all. The age is known when there is a birth year. With the
// rest missing the app assumes the youngest age that fits (PLAN.md,
// "Children under 13"): a year alone means born on Dec 31 of that year, a
// year and month means the last day of that month. A birthday with no year
// is unknown. Only today's date counts, not the time.
func AgeOn(birth dates.Partial, today time.Time) (age int, known bool) {
	if birth.Year == 0 {
		return 0, false
	}
	month, day := birth.Month, birth.Day
	if month < 1 || month > 12 {
		month, day = 12, 31
	} else if day == 0 {
		// Day 0 of the next month is the last day of this one.
		day = time.Date(birth.Year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
	y, m, d := today.Date()
	age = y - birth.Year
	if int(m) < month || (int(m) == month && d < day) {
		age--
	}
	if age < 0 {
		age = 0
	}
	return age, true
}

// KnownUnder13 reports whether the person's age is known and under 13.
func KnownUnder13(birth dates.Partial, today time.Time) bool {
	age, known := AgeOn(birth, today)
	return known && age < Under13
}

// AgeKnown reports whether there is a birth year.
func AgeKnown(birth dates.Partial) bool { return birth.Year != 0 }
