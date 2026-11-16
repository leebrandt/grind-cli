// Package report computes the `grind hwd` weekly report.
//
// It is a pure package: a report is a function of .projects.json and one
// instant. There is no Service and no Clock, because the instant is passed in
// — the same shape as invoice.Generate. That keeps the week-boundary,
// ordering and money rules testable without arranging a filesystem or a clock.
//
// The package is read-only by construction: it has no git dependency, so
// there is no code path here that could write or commit.
package report

import (
	"sort"
	"time"

	"github.com/leebrandt/grind/internal/config"
	"github.com/leebrandt/grind/internal/workspace"
)

// Options selects which week to report. WeeksBack 0 is the current week;
// N >= 1 is the complete week N weeks back.
type Options struct {
	WeeksBack int
}

// Row is one project's line in the hours table.
type Row struct {
	Name            string
	Status          string // "" | "published" | "canceled"
	LongTerm        bool
	WorkedSeconds   int64   // Σ Session.Duration in the window
	BillableSeconds int64   // Σ Session.Rounded in the window
	Amount          float64 // billableSeconds / 3600 × rate
	RunningNow      bool    // any session with End == nil (any week)
	HasUnbilled     bool    // any ended, un-invoiced session (any week)
}

// Totals is the table's TOTAL row, in raw units so nothing is summed from
// display strings.
type Totals struct {
	WorkedSeconds   int64
	BillableSeconds int64
	Amount          float64
}

// Money is the invoiced/un-invoiced split of the week. Billable is not stored
// here: it is Report.Total, so the money block's Billable line and the table's
// TOTAL row are the same numbers by construction. Unbilled is Billable minus
// Invoiced, computed once, so the three lines always add up.
type Money struct {
	InvoicedSeconds int64
	InvoicedAmount  float64
	UnbilledSeconds int64
	UnbilledAmount  float64
}

// Finished is one project that finished inside the window.
type Finished struct {
	Name          string
	Status        string    // "published" | "canceled"
	Date          time.Time // the timestamp, in the window's local zone
	WorkedSeconds int64     // the project's hours for the week
	HasWork       bool      // whether the project has a row in the table
}

// FinishedEarlier is a project that finished but has no recorded timestamp.
type FinishedEarlier struct {
	Name   string
	Status string
}

// Report is everything one run of the command renders.
type Report struct {
	Start time.Time
	End   time.Time
	Past  bool

	Rows     []Row
	Total    Totals
	Money    Money
	NoRate   []string // names of zero-rate projects with a row, sorted
	Finished []Finished
	Earlier  []FinishedEarlier
}

// Window returns the half-open-or-closed reporting window. For the current
// week (weeksBack <= 0) it is [Sunday 00:00 local, now], with now included.
// For a past week it is [start, start+7d), with the end EXCLUSIVE so a session
// starting at exactly Sunday midnight belongs to the newer week and is never
// counted twice.
//
// past reports which shape it is; it is what the header and the two "this
// week"/"that week" strings switch on.
func Window(now time.Time, weeksBack int) (start, end time.Time, past bool) {
	// Midnight today in the workspace's local zone, then back up to the most
	// recent Sunday. time.Weekday() has Sunday == 0, so subtracting the
	// weekday lands on Sunday without a lookup table.
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start = day.AddDate(0, 0, -int(day.Weekday()))
	if weeksBack <= 0 {
		return start, now, false
	}
	// AddDate, not 24-hour multiples: a week that spans a daylight-saving
	// change still begins and ends at local midnight.
	start = start.AddDate(0, 0, -7*weeksBack)
	return start, start.AddDate(0, 0, 7), true
}

// InWindow reports whether t falls in the window. The current week includes
// its last instant (now); a past week excludes it (the following Sunday
// midnight), which is what keeps the two reports disjoint.
func InWindow(t, start, end time.Time, past bool) bool {
	if t.Before(start) {
		return false
	}
	if past {
		return t.Before(end)
	}
	return !t.After(end)
}

// HeaderLine is the report's first line. The current week ends "through now"
// because it is still running; a past week names both end dates, and the last
// named day is the last day actually in the week.
func HeaderLine(start, end time.Time, past bool) string {
	if !past {
		return "Week: " + start.Format("2006-01-02 (Mon)") + " through now"
	}
	last := end.AddDate(0, 0, -1)
	return "Week: " + start.Format("2006-01-02 (Mon)") + " through " + last.Format("2006-01-02 (Mon)")
}

// Build reads .projects.json and computes the report for opts at now. now is
// the single instant every window decision uses; nothing here reads a clock.
func Build(ws *workspace.Workspace, opts Options, now time.Time) (*Report, error) {
	projects, err := config.ReadProjects(ws.ProjectsConfigPath())
	if err != nil {
		return nil, err
	}

	start, end, past := Window(now, opts.WeeksBack)
	rep := &Report{Start: start, End: end, Past: past}

	// Pass 1 — rows, totals, money, zero-rate names.
	//
	// The two workspace-wide flags (runningNow, hasUnbilled) are set BEFORE
	// the window check: they answer "what is this project doing right now",
	// not "what did it do this week", so a session in any week can set them.
	// The window check only gates the accumulating counters and `has`.
	for name, entry := range projects.Projects {
		var (
			worked      int64
			billable    int64
			invoiced    int64
			runningNow  bool
			hasUnbilled bool
			has         bool
		)
		for _, session := range entry.Sessions {
			if session.End == nil {
				runningNow = true
			} else if !session.Invoiced {
				hasUnbilled = true
			}
			if !InWindow(session.Start, start, end, past) {
				continue
			}
			has = true
			worked += session.Duration
			billable += session.Rounded
			if session.Invoiced {
				invoiced += session.Rounded
			}
		}
		// A project has a row iff it has an in-window session. A project
		// whose only in-window session is still running still qualifies —
		// that is what makes the 0.00h row appear.
		if !has {
			continue
		}

		row := Row{
			Name:            name,
			Status:          entry.Status,
			LongTerm:        entry.LongTerm,
			WorkedSeconds:   worked,
			BillableSeconds: billable,
			Amount:          float64(billable) / 3600 * entry.Billing.Rate,
			RunningNow:      runningNow,
			HasUnbilled:     hasUnbilled,
		}
		rep.Rows = append(rep.Rows, row)

		rep.Total.WorkedSeconds += worked
		rep.Total.BillableSeconds += billable
		rep.Total.Amount += row.Amount
		rep.Money.InvoicedSeconds += invoiced
		rep.Money.InvoicedAmount += float64(invoiced) / 3600 * entry.Billing.Rate

		// A zero-rate project is counted, not hidden: its hours are in the
		// table and in Total, only its money is zero. Naming it here is what
		// lets the renderer explain the missing amount.
		if entry.Billing.Rate == 0 {
			rep.NoRate = append(rep.NoRate, name)
		}
	}

	// Unbilled is derived once by subtraction, so Invoiced + Unbilled ==
	// Billable exactly, with no rounding gap.
	rep.Money.UnbilledSeconds = rep.Total.BillableSeconds - rep.Money.InvoicedSeconds
	rep.Money.UnbilledAmount = rep.Total.Amount - rep.Money.InvoicedAmount

	// Worked time descending, name ascending as the deterministic tie-break.
	// The sort key is the table's first data column, so the visible order
	// matches what the eye reads first.
	sort.Slice(rep.Rows, func(i, j int) bool {
		if rep.Rows[i].WorkedSeconds != rep.Rows[j].WorkedSeconds {
			return rep.Rows[i].WorkedSeconds > rep.Rows[j].WorkedSeconds
		}
		return rep.Rows[i].Name < rep.Rows[j].Name
	})
	sort.Strings(rep.NoRate)

	// Pass 2 — finished and earlier.
	workedByName := make(map[string]int64, len(rep.Rows))
	for _, r := range rep.Rows {
		workedByName[r.Name] = r.WorkedSeconds
	}
	for name, entry := range projects.Projects {
		// The timestamp that matters is the one matching the CURRENT status,
		// so the status word and the date on the finished line always
		// describe the same event.
		var ts *time.Time
		switch entry.Status {
		case "published":
			ts = entry.PublishedAt
		case "canceled":
			ts = entry.CanceledAt
		default:
			continue // never finished; not "earlier" either
		}
		if ts == nil {
			rep.Earlier = append(rep.Earlier, FinishedEarlier{Name: name, Status: entry.Status})
			continue
		}
		if !InWindow(*ts, start, end, past) {
			continue // finished, but not this week
		}
		worked, has := workedByName[name]
		rep.Finished = append(rep.Finished, Finished{
			Name:          name,
			Status:        entry.Status,
			Date:          ts.In(now.Location()),
			WorkedSeconds: worked,
			HasWork:       has,
		})
	}
	sort.Slice(rep.Earlier, func(i, j int) bool { return rep.Earlier[i].Name < rep.Earlier[j].Name })
	sort.Slice(rep.Finished, func(i, j int) bool { return rep.Finished[i].Name < rep.Finished[j].Name })

	return rep, nil
}
