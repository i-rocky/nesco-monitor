package main

import (
	"testing"
	"time"

	_ "time/tzdata"
)

func mustDhaka(t *testing.T) *time.Location {
	t.Helper()
	tz, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	return tz
}

func TestNextTick(t *testing.T) {
	dhaka := mustDhaka(t)

	cases := []struct {
		name      string
		now       time.Time
		wantDay   int
		wantHour  int
		wantMin   int
	}{
		{
			name:     "midnight rolls to today 05:00",
			now:      time.Date(2026, 5, 18, 0, 30, 0, 0, dhaka),
			wantDay:  18,
			wantHour: 5,
			wantMin:  0,
		},
		{
			name:     "exactly at 05:00 rolls to tomorrow",
			now:      time.Date(2026, 5, 18, 5, 0, 0, 0, dhaka),
			wantDay:  19,
			wantHour: 5,
			wantMin:  0,
		},
		{
			name:     "afternoon rolls to tomorrow 05:00",
			now:      time.Date(2026, 5, 18, 15, 0, 0, 0, dhaka),
			wantDay:  19,
			wantHour: 5,
			wantMin:  0,
		},
		{
			name:     "late evening rolls to tomorrow",
			now:      time.Date(2026, 5, 18, 23, 59, 0, 0, dhaka),
			wantDay:  19,
			wantHour: 5,
			wantMin:  0,
		},
		{
			name: "UTC input converted to Dhaka — 18:00 UTC = 00:00 Dhaka next day, " +
				"so next tick is that same Dhaka day's 05:00",
			now:      time.Date(2026, 5, 17, 18, 0, 0, 0, time.UTC),
			wantDay:  18,
			wantHour: 5,
			wantMin:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NextTick(tc.now, dhaka)
			if got.Location().String() != "Asia/Dhaka" {
				t.Errorf("location = %v, want Asia/Dhaka", got.Location())
			}
			if got.Hour() != tc.wantHour || got.Minute() != tc.wantMin {
				t.Errorf("time-of-day = %02d:%02d, want %02d:%02d (got %s)",
					got.Hour(), got.Minute(), tc.wantHour, tc.wantMin, got.Format(time.RFC3339))
			}
			if got.Day() != tc.wantDay {
				t.Errorf("day = %d, want %d (got %s)", got.Day(), tc.wantDay, got.Format(time.RFC3339))
			}
			if !got.After(tc.now) {
				t.Errorf("tick %s not strictly after now %s", got, tc.now)
			}
		})
	}
}
