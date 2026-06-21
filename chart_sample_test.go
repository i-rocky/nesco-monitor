package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "time/tzdata"
)

// TestRenderSampleCharts is a manual-inspection harness. Run with:
//
//	go test -run TestRenderSampleCharts -v
//
// Spending for day D needs TWO consecutive balance readings (D and D+1),
// so N+1 daily readings produce N spending data points on the chart. The
// scenarios below exercise that off-by-one explicitly.
func TestRenderSampleCharts(t *testing.T) {
	if err := os.MkdirAll("samples", 0o755); err != nil {
		t.Fatal(err)
	}
	dhaka, err := time.LoadLocation("Asia/Dhaka")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().In(dhaka)
	monthStart := time.Date(now.Year(), now.Month(), 1, 1, 10, 0, 0, dhaka)
	day := func(n int) time.Time { return monthStart.AddDate(0, 0, n-1) }

	cases := []struct {
		name      string
		points    []HistoryPoint
		recharges []Recharge
	}{
		{
			name:   "00-empty",
			points: nil,
		},
		{
			// 1 balance reading → 0 spending data points → empty chart.
			name: "01-one-reading-no-spending-yet",
			points: []HistoryPoint{
				dailyReading(day(4), 2500),
			},
		},
		{
			// 2 readings on adjacent days → 1 spending data point → just a dot.
			name: "02-one-dot-no-line",
			points: []HistoryPoint{
				dailyReading(day(4), 2500),
				dailyReading(day(5), 2050),
			},
		},
		{
			// 3 adjacent readings → 2 spending points connected by a line.
			name: "03-two-dots-line",
			points: []HistoryPoint{
				dailyReading(day(4), 2500),
				dailyReading(day(5), 2050),
				dailyReading(day(6), 1700),
			},
		},
		{
			// Multiple readings but with a missing-day gap in the middle:
			// line breaks across the missing day.
			name: "04-two-segments-gap",
			points: []HistoryPoint{
				dailyReading(day(4), 2500),
				dailyReading(day(5), 2050),
				dailyReading(day(6), 1700),
				// day 7 missing
				dailyReading(day(8), 900),
				dailyReading(day(9), 500),
			},
		},
		{
			// Recharge day: balance jumps up, so that day's spending can't
			// be computed → gap in the line.
			name:   "05-full-month-gap-and-recharge",
			points: dailyRunWithGapAndRecharge(monthStart, now),
		},
		{
			// Recharge day filled in via recharge energy; a cache-stale dup
			// day collapses to a gap instead of a spike.
			name: "06-recharge-and-stale",
			points: []HistoryPoint{
				dailyReading(day(4), 600),
				dailyReading(day(5), 350),
				dailyReading(day(6), 3900), // recharge applied
				dailyReading(day(7), 3650),
				dailyReading(day(8), 3650), // cache dup -> gap
				dailyReading(day(9), 3400),
			},
			recharges: []Recharge{
				{OrderID: "s1", EnergyAmount: 3800, PurchasedAt: day(5).Add(2 * time.Hour).UTC()},
			},
		},
	}

	for _, c := range cases {
		png, err := RenderMonthlyPNG(c.points, c.recharges, dhaka)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		writeSample(t, filepath.Join("samples", "monthly_"+c.name+".png"), png)
	}
}

func dailyReading(at time.Time, balance float64) HistoryPoint {
	return HistoryPoint{FetchedAt: at.UTC(), Balance: balance}
}

func dailyRunWithGapAndRecharge(monthStart, until time.Time) []HistoryPoint {
	rnd := rand.New(rand.NewSource(7))
	tz := monthStart.Location()
	skipDay := 9
	rechargeDay := 15

	bal := 4200.0
	var out []HistoryPoint
	day := 0
	for d := monthStart; !d.After(until); d = d.AddDate(0, 0, 1) {
		day++
		if day == skipDay {
			bal -= 350 + rnd.Float64()*200 // still consume even though monitor is "down"
			continue
		}
		stamp := time.Date(d.Year(), d.Month(), d.Day(), 1, 10, 0, 0, tz)
		out = append(out, dailyReading(stamp, bal))
		drain := 350 + rnd.Float64()*200
		bal -= drain
		if day == rechargeDay {
			bal += 2500
		}
	}
	return out
}

func writeSample(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(data))
}
