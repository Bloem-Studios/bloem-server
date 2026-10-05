// Package bloemtestclock supplies deterministic clocks to Bloem regression fixtures.
package bloemtestclock

import "time"

// Fixed returns the fixture instant without consulting the wall clock.
type Fixed time.Time

func (c Fixed) Now() time.Time { return time.Time(c) }
