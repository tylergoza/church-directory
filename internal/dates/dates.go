// Package dates holds partial dates: a birthday or anniversary where the
// month, day and year can each be left empty (a day needs a month). A part
// that is empty is 0.
package dates

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// MinYear is the earliest year accepted. The latest is the current year.
const MinYear = 1850

// Partial is a date with any of its parts missing.
type Partial struct {
	Month, Day, Year int
}

// Shown says which parts of a date may be displayed. Display never shows a
// day without a month, whatever Day says.
type Shown struct {
	Month, Day, Year bool
}

// All shows every part.
var All = Shown{Month: true, Day: true, Year: true}

var monthNames = [...]string{"", "January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December"}

// MonthName is "March" for 3, "" for anything outside 1 to 12.
func MonthName(m int) string {
	if m < 1 || m > 12 {
		return ""
	}
	return monthNames[m]
}

// MonthNames lists January to December, for a month select.
func MonthNames() []string { return monthNames[1:] }

// IsZero reports whether no part is filled in.
func (p Partial) IsZero() bool { return p.Month == 0 && p.Day == 0 && p.Year == 0 }

// Parse reads the three text parts of a form. Blank parts are empty;
// anything that isn't a whole number is an error.
func Parse(month, day, year string) (Partial, error) {
	var p Partial
	var err error
	if p.Month, err = part(month); err != nil {
		return Partial{}, errors.New("the month isn't a number")
	}
	if p.Day, err = part(day); err != nil {
		return Partial{}, errors.New("the day isn't a number")
	}
	if p.Year, err = part(year); err != nil {
		return Partial{}, errors.New("the year isn't a number")
	}
	return p, nil
}

func part(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

var daysIn = [...]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

func leap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

// Validate checks the date: the month is 1 to 12, a day needs a month and
// fits it, Feb 29 is fine with no year but otherwise only in leap years,
// and a year is between MinYear and this year.
func (p Partial) Validate() error {
	if p.Month < 0 || p.Month > 12 {
		return errors.New("the month must be 1 to 12")
	}
	if p.Day < 0 || p.Day > 31 {
		return errors.New("the day must be 1 to 31")
	}
	if p.Day != 0 && p.Month == 0 {
		return errors.New("pick a month to go with the day")
	}
	if p.Year != 0 && (p.Year < MinYear || p.Year > time.Now().Year()) {
		return errors.New("the year must be between " + strconv.Itoa(MinYear) + " and " + strconv.Itoa(time.Now().Year()))
	}
	if p.Day != 0 {
		max := daysIn[p.Month]
		if p.Month == 2 && p.Year != 0 && !leap(p.Year) {
			max = 28
		}
		if p.Day > max {
			return errors.New(MonthName(p.Month) + " doesn't have " + strconv.Itoa(p.Day) + " days" + inYear(p))
		}
	}
	return nil
}

func inYear(p Partial) string {
	if p.Month == 2 && p.Day == 29 && p.Year != 0 {
		return " in " + strconv.Itoa(p.Year)
	}
	return ""
}

// Format writes the parts that are both filled in and shown:
// "March 14", "March", "March 14, 1985", "March 1985", "1985" or "".
func (p Partial) Format(show Shown) string {
	month := p.Month >= 1 && p.Month <= 12 && show.Month
	day := month && p.Day != 0 && show.Day
	year := p.Year != 0 && show.Year
	var b strings.Builder
	if month {
		b.WriteString(monthNames[p.Month])
	}
	if day {
		b.WriteString(" " + strconv.Itoa(p.Day))
	}
	if year {
		if day {
			b.WriteString(",")
		}
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strconv.Itoa(p.Year))
	}
	return b.String()
}

// String shows every part.
func (p Partial) String() string { return p.Format(All) }
