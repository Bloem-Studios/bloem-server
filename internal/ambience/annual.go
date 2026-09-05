package ambience

import "time"

// At resolves an active occurrence to absolute UTC bounds. Clients receive only
// this occurrence, so expiry works without interpreting recurrence rules.
// Feb 29 occurrences are skipped in years where that date does not exist.
func (w Window) At(now time.Time) (Window, bool) {
	if !w.RepeatYearly {
		return w, w.Contains(now)
	}
	if now.Before(w.StartsAt) {
		return Window{}, false
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return Window{}, false
	}
	start, end := w.StartsAt.In(loc), w.EndsAt.In(loc)
	for _, year := range []int{now.In(loc).Year() - 1, now.In(loc).Year()} {
		if year < start.Year() {
			continue
		}
		a, okA := anniversary(start, year)
		b, okB := anniversary(end, year+end.Year()-start.Year())
		occurrence := Window{StartsAt: a.UTC(), EndsAt: b.UTC()}
		if okA && okB && occurrence.Contains(now) {
			return occurrence, true
		}
	}
	return Window{}, false
}
func anniversary(t time.Time, year int) (time.Time, bool) {
	next := time.Date(year, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	// Missing dates and DST gap wall times do not become a different schedule.
	return next, next.Month() == t.Month() && next.Day() == t.Day() && next.Hour() == t.Hour() && next.Minute() == t.Minute()
}
func activeWire(packs []Pack, now time.Time) []Wire {
	out := make([]Wire, 0, len(packs))
	for _, p := range packs {
		if window, ok := p.Window.At(now); ok {
			p.Window = window
			out = append(out, p.Wire())
		}
	}
	return out
}
