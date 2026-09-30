// Package clock is grind's only way of finding out what time it is.
//
// Why this exists: code that reads the wall clock directly is code that
// cannot be tested at a known time. A test has to either accept whatever
// the clock says, or call time.Now() itself and hope the two calls land
// close enough. Both are fragile, and both have bitten this codebase —
// the status tests once asserted against hardcoded dates and started
// failing as the calendar moved on.
//
// So grind does not call time.Now(). It calls a Clock, and production
// wiring hands it a Real. A test hands it a Fake and sets the time.
// That turns "what does grind do at 3am on a Sunday" from an
// unanswerable question into a one-line setup.
//
// The interface has exactly one method because that is all grind needs.
// The bigger ecosystem versions of this (clockwork, benbjohnson/clock)
// also carry After, Sleep, Timer and friends, because their users have
// goroutines that wait on time. grind has none of those, so they are
// not here. A seam should be as small as the dependency really is.
package clock

import "time"

// Clock tells you what time it is. Real answers with the wall clock;
// Fake answers with whatever the test set.
type Clock interface {
	Now() time.Time
}

// Real is the wall clock. It is what production uses, and its zero value
// is ready to use, so wiring it is just `Clock: clock.Real{}`.
type Real struct{}

// Now returns the current wall-clock time.
func (Real) Now() time.Time { return time.Now() }

// Fake is a clock a test drives by hand. Time does not move on its own:
// it only changes when the test sets T or calls Advance. That is the
// whole point — a test that wants "three days later" says so, rather
// than hoping the test suite is still running tomorrow.
//
// Fake is safe to read from many goroutines, but only the test goroutine
// should move time.
type Fake struct {
	// T is the current fake time. Set it directly to jump to an instant.
	T time.Time
}

// NewFake returns a Fake set to t.
func NewFake(t time.Time) *Fake { return &Fake{T: t} }

// Now returns the fake time.
func (f *Fake) Now() time.Time { return f.T }

// Advance moves the fake clock forward by d. A negative d moves it
// backwards, which is occasionally useful for testing a clock change.
func (f *Fake) Advance(d time.Duration) { f.T = f.T.Add(d) }
