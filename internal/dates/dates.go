// Package dates parses due-date inputs into local YYYY-MM-DD strings.
//
// The accepted forms are deliberately small: relative words ("today",
// "tomorrow"), relative durations ("3d", "1w"), and compact absolute forms
// ("0720", "072026", "20260720", "2026-07-20"). Relative forms are computed
// from `now` in LOCAL time — the result is a local date, not UTC. This
// deliberately fixes v1's known bug where "due today" was computed against
// UTC and mislabeled tasks in negative-offset timezones.
//
// The package is pure: no I/O, no dependencies beyond time and grinderr.
// The journal slice will reuse it.
package dates

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/leebrandt/grind/internal/grinderr"
)

// relativeRe matches the relative duration forms: a number followed by
// d/days (days) or w/week (weeks).
var relativeRe = regexp.MustCompile(`^(\d+)(d|days|w|week)$`)

// ParseDate parses a due-date input into a local YYYY-MM-DD string. now is
// injectable so tests can fix the reference date.
//
// Accepted forms (case-insensitive, leading/trailing whitespace trimmed):
//
//	today, tomorrow
//	3d / 3days, 1w / 1week
//	0720        July 20 of the current year (MMDD)
//	072026      July 20, 2026 (MMDDYY)
//	20260720    July 20, 2026 (YYYYMMDD)
//	2026-07-20  July 20, 2026 (ISO)
//
// Absolute forms are validated against the real calendar (month 1–12, day
// valid for the month and year, leap years included). Any failure —
// unparseable format or impossible date — is a user error echoing the
// original input.
func ParseDate(input string, now time.Time) (string, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return "", dateError(input)
	}

	switch s {
	case "today":
		return now.Format("2006-01-02"), nil
	case "tomorrow":
		return now.AddDate(0, 0, 1).Format("2006-01-02"), nil
	}

	if m := relativeRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		// A zero amount ("0d") is a typo, not a date; reject it.
		if err != nil || n <= 0 {
			return "", dateError(input)
		}
		days := n
		switch m[2] {
		case "w", "week":
			days = n * 7
		}
		return now.AddDate(0, 0, days).Format("2006-01-02"), nil
	}

	// Absolute forms. The digit-count switch keeps the parsing explicit:
	// 4 digits are MMDD, 6 are MMDDYY, 8 are YYYYMMDD.
	switch len(s) {
	case 4:
		// MMDD — July 20 of the current year.
		month, err1 := strconv.Atoi(s[0:2])
		day, err2 := strconv.Atoi(s[2:4])
		if err1 != nil || err2 != nil {
			return "", dateError(input)
		}
		return validateDate(input, now.Year(), month, day)
	case 6:
		// MMDDYY — July 20, 2026.
		month, err1 := strconv.Atoi(s[0:2])
		day, err2 := strconv.Atoi(s[2:4])
		yy, err3 := strconv.Atoi(s[4:6])
		if err1 != nil || err2 != nil || err3 != nil {
			return "", dateError(input)
		}
		return validateDate(input, 2000+yy, month, day)
	case 8:
		// YYYYMMDD — July 20, 2026.
		year, err1 := strconv.Atoi(s[0:4])
		month, err2 := strconv.Atoi(s[4:6])
		day, err3 := strconv.Atoi(s[6:8])
		if err1 != nil || err2 != nil || err3 != nil {
			return "", dateError(input)
		}
		return validateDate(input, year, month, day)
	}

	// ISO YYYY-MM-DD. time.Parse validates the calendar itself, so an
	// impossible date like 2026-02-30 is rejected here rather than
	// normalizing to March 2.
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2006-01-02"), nil
	}

	return "", dateError(input)
}

// validateDate checks that month/day is a real calendar date in year and
// returns it as YYYY-MM-DD. time.Date normalizes out-of-range values (Feb
// 30 becomes Mar 2), so the normalized result is compared against the
// input to catch impossible dates. UTC is used for the check so no
// timezone or DST rule can interfere.
func validateDate(input string, year, month, day int) (string, error) {
	if month < 1 || month > 12 {
		return "", dateError(input)
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return "", dateError(input)
	}
	return t.Format("2006-01-02"), nil
}

// dateError builds the user error for an unparseable or impossible date.
// The original input is echoed back so the user can see exactly what was
// rejected.
func dateError(input string) error {
	return grinderr.NewUser(fmt.Sprintf("Unparseable date: %q", input))
}

// TimeAgo renders t relative to now in v1's format: "just now" under a
// minute, then "5m ago", "3h ago", "2d ago", "4mo ago", "1y ago" (floored
// units). now is injectable so tests can fix the reference time.
//
// The thresholds mirror v1's timeAgo exactly: <60s → "just now"; <60m →
// "Xm ago"; <24h → "Xh ago"; <30d → "Xd ago"; <12mo → "Xmo ago"; else
// "Xy ago". Months are 30-day buckets and years are 12-month buckets, so
// the buckets line up with v1's math even across calendar quirks.
func TimeAgo(t, now time.Time) string {
	seconds := int64(now.Sub(t) / time.Second)
	if seconds < 60 {
		// This also catches future timestamps (negative delta): v1's
		// `seconds < 60` did the same, so a clock skew or a session
		// timestamped ahead of now reads "just now", never a negative
		// "ago".
		return "just now"
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm ago", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh ago", hours)
	}
	days := hours / 24
	if days < 30 {
		return fmt.Sprintf("%dd ago", days)
	}
	months := days / 30
	if months < 12 {
		return fmt.Sprintf("%dmo ago", months)
	}
	return fmt.Sprintf("%dy ago", months/12)
}