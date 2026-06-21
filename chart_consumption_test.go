package main

import (
	"testing"
	"time"

	_ "time/tzdata"
)

// reading builds a HistoryPoint stamped at 01:10 on the given Dhaka day.
func reading(tz *time.Location, day int, bal float64) HistoryPoint {
	return HistoryPoint{FetchedAt: time.Date(2026, 6, day, 1, 10, 0, 0, tz).UTC(), Balance: bal}
}

// TestDailySpending_RealJune runs the actual June 2026 daily series for
// account 12345678 — including the cache-stale duplicate days (14/15, 17) and
// the two recharge days — through dailySpending and checks it produces a sane,
// gap-free line.
func TestDailySpending_RealJune(t *testing.T) {
	tz, _ := time.LoadLocation("Asia/Dhaka")

	points := []HistoryPoint{
		reading(tz, 1, 1460.29), reading(tz, 2, 1378.24), reading(tz, 3, 1231.60),
		reading(tz, 4, 1035.03), reading(tz, 5, 785.32), reading(tz, 6, 557.42),
		reading(tz, 7, 346.34),
		reading(tz, 8, 3635.54), // Jun 7 recharge applied
		reading(tz, 9, 3310.16), reading(tz, 10, 3011.49), reading(tz, 11, 2738.17),
		reading(tz, 12, 2497.06), reading(tz, 13, 2250.54),
		reading(tz, 14, 2250.54), reading(tz, 15, 2250.54), // cache-stale dups
		reading(tz, 16, 1130.69),
		reading(tz, 17, 1130.69), // cache-stale dup
		reading(tz, 18, 648.22), reading(tz, 19, -44.13),
		reading(tz, 20, 98.88), // Jun 19 recharge applied
		reading(tz, 21, -253.67),
	}
	recharges := []Recharge{
		{OrderID: "jun7", EnergyAmount: 3580.07, PurchasedAt: time.Date(2026, 6, 7, 1, 12, 0, 0, tz).UTC()},
		{OrderID: "jun19", EnergyAmount: 478.56, PurchasedAt: time.Date(2026, 6, 19, 1, 13, 0, 0, tz).UTC()},
		// Today's recharge, purchased in the afternoon — must NOT leak into
		// Jun 20's spending (it's after the Jun 21 reading, which has no successor).
		{OrderID: "jun21", EnergyAmount: 1914.24, PurchasedAt: time.Date(2026, 6, 21, 13, 17, 0, 0, tz).UTC()},
	}

	spend := dailySpending(points, recharges, tz)
	day := func(d int) time.Time { return time.Date(2026, 6, d, 0, 0, 0, 0, tz) }
	approx := func(got, want float64) bool { return got-want < 0.01 && want-got < 0.01 }

	// Every day Jun 1..Jun 20 must have a value (no gaps); Jun 21 is the last
	// reading so it has no spending yet.
	for d := 1; d <= 20; d++ {
		if _, ok := spend[day(d)]; !ok {
			t.Errorf("Jun %d missing — chart would show a gap", d)
		}
	}
	if _, ok := spend[day(21)]; ok {
		t.Errorf("Jun 21 should have no spending (latest reading)")
	}

	// Recharge days reflect real consumption, not the balance jump.
	if v := spend[day(7)]; !approx(v, 290.87) {
		t.Errorf("Jun 7 (recharge) = %v, want 290.87", v)
	}
	if v := spend[day(19)]; !approx(v, 335.55) {
		t.Errorf("Jun 19 (recharge) = %v, want 335.55", v)
	}
	// Cache-stale run Jun 13-15 distributes (2250.54-1130.69)/3 = 373.28/day —
	// no fake 0s, no ~1120 catch-up spike.
	for _, d := range []int{13, 14, 15} {
		if v := spend[day(d)]; !approx(v, 373.28) {
			t.Errorf("Jun %d (stale, distributed) = %v, want 373.28", d, v)
		}
	}
	// Cache-stale run Jun 16-17 distributes (1130.69-648.22)/2 = 241.24/day.
	for _, d := range []int{16, 17} {
		if v := spend[day(d)]; !approx(v, 241.24) {
			t.Errorf("Jun %d (stale, distributed) = %v, want 241.24", d, v)
		}
	}
	// Jun 20 = 98.88-(-253.67) = 352.55, with today's afternoon recharge NOT leaking.
	if v := spend[day(20)]; !approx(v, 352.55) {
		t.Errorf("Jun 20 = %v, want 352.55 (afternoon recharge must not leak)", v)
	}
	// Nothing implausible. Jun 18 (648.22-(-44.13)=692.35) is the one real high day.
	for d, v := range spend {
		if v > 750 {
			t.Errorf("implausible spike %v on %s", v, d.Format("Jan 2"))
		}
	}
}
