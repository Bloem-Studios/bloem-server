package ambience

import (
	"testing"
	"time"
)

func instant(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		panic(err)
	}
	return t
}
func TestAnnualWindow(t *testing.T) {
	w := Window{StartsAt: instant("2026-11-30T23:00:00Z"), EndsAt: instant("2026-12-31T23:00:00Z"), RepeatYearly: true, Timezone: "Europe/Amsterdam"}
	for _, tc := range []struct {
		at     string
		active bool
	}{
		{"2025-12-15T00:00:00Z", false}, {"2027-11-30T22:59:59Z", false},
		{"2027-11-30T23:00:00Z", true}, {"2027-12-31T22:59:59Z", true}, {"2027-12-31T23:00:00Z", false},
	} {
		got, ok := w.At(instant(tc.at))
		if ok != tc.active {
			t.Errorf("%s: active=%v", tc.at, ok)
		}
		if ok && (got.RepeatYearly || got.StartsAt.Year() != 2027) {
			t.Errorf("must project concrete occurrence: %+v", got)
		}
	}
}
func TestAnnualWindowCrossYearAndLeap(t *testing.T) {
	w := Window{StartsAt: instant("2026-12-01T00:00:00Z"), EndsAt: instant("2027-01-07T00:00:00Z"), RepeatYearly: true, Timezone: "UTC"}
	got, ok := w.At(instant("2029-01-01T00:00:00Z"))
	if !ok || got.StartsAt.Year() != 2028 || got.EndsAt.Year() != 2029 {
		t.Fatal(got, ok)
	}
	leap := Window{StartsAt: instant("2028-02-29T00:00:00Z"), EndsAt: instant("2028-03-02T00:00:00Z"), RepeatYearly: true, Timezone: "UTC"}
	if _, ok := leap.At(instant("2029-03-01T00:00:00Z")); ok {
		t.Fatal("invalid calendar date must skip occurrence")
	}
}

func TestAnnualValidationAndDST(t *testing.T) {
	in := Input{EffectID: "snow", Window: Window{StartsAt: instant("2026-12-01T00:00:00Z"), EndsAt: instant("2027-01-01T00:00:00Z"), RepeatYearly: true, Timezone: "Unknown/Zone"}}
	if _, err := Normalize(in); err == nil {
		t.Fatal("unknown zone accepted")
	}
	in.Window.Timezone = "UTC"
	in.Window.EndsAt = in.Window.StartsAt.AddDate(1, 0, 0)
	if _, err := Normalize(in); err == nil {
		t.Fatal("full-year recurrence accepted")
	}
	w := Window{StartsAt: instant("2026-03-01T09:00:00Z"), EndsAt: instant("2026-04-01T08:00:00Z"), RepeatYearly: true, Timezone: "Europe/Amsterdam"}
	got, ok := w.At(instant("2027-03-15T00:00:00Z"))
	if !ok || got.EndsAt != instant("2027-04-01T08:00:00Z") {
		t.Fatal(got, ok)
	}
}
