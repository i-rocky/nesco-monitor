package main

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	"github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"
)

// Discord-friendly palette.
var (
	bgColor   = drawing.Color{R: 47, G: 49, B: 54, A: 255}    // Discord dark bg
	gridColor = drawing.Color{R: 80, G: 84, B: 92, A: 255}    // subtle gridlines
	axisColor = drawing.Color{R: 200, G: 200, B: 200, A: 255} // axes/strokes
	lineColor = drawing.Color{R: 88, G: 166, B: 255, A: 255}  // Discord-ish blue
	avgColor  = drawing.Color{R: 237, G: 178, B: 84, A: 255}  // warm yellow
	textColor = drawing.ColorWhite
)

// maxSpanDays bounds how many days of consumption we'll spread across a span
// between two readings. Cache staleness and short monitor downtime (a few
// days) get distributed into a smooth line; a longer span is treated as a
// genuine outage and left as a gap rather than inventing a week of data.
const maxSpanDays = 7

// dailySpending reduces readings to one per local day (latest), drops
// cache-stale duplicate days, and returns spending per day keyed by local
// midnight. For each span between two consecutive present readings, total
// consumption = balance(start) - balance(end) + recharge energy credited in
// the span; that total is distributed evenly across the days of the span.
// This fills cache-stale and missing days with a sensible estimate instead of
// leaving holes, while recharge energy is added back so recharge days reflect
// real consumption rather than a balance jump.
func dailySpending(points []HistoryPoint, recharges []Recharge, tz *time.Location) map[time.Time]float64 {
	type dayReading struct {
		At      time.Time
		Balance float64
	}
	byDay := map[time.Time]dayReading{}
	for _, p := range points {
		key := dayKey(p.FetchedAt.In(tz), tz)
		if existing, ok := byDay[key]; !ok || p.FetchedAt.After(existing.At) {
			byDay[key] = dayReading{At: p.FetchedAt, Balance: p.Balance}
		}
	}

	// Drop cache-stale days: a day whose balance exactly equals the previously
	// retained day's balance is a cached repeat (real consumption is never
	// exactly zero). Removing it folds those days into the following span so
	// their consumption is distributed. A recharge always changes the balance,
	// so this never removes a recharge day.
	var days []time.Time
	for d := range byDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })
	kept := days[:0]
	var lastKept float64
	haveKept := false
	for _, d := range days {
		b := byDay[d].Balance
		if haveKept && b == lastKept {
			delete(byDay, d)
			continue
		}
		lastKept = b
		haveKept = true
		kept = append(kept, d)
	}

	out := map[time.Time]float64{}
	for i := 0; i+1 < len(kept); i++ {
		d0, d1 := kept[i], kept[i+1]
		spanDays := int(d1.Sub(d0).Hours()/24 + 0.5)
		if spanDays < 1 || spanDays > maxSpanDays {
			continue // genuine outage — leave a gap
		}
		total := byDay[d0].Balance - byDay[d1].Balance +
			rechargeEnergyBetween(recharges, byDay[d0].At, byDay[d1].At)
		if total < 0 {
			continue
		}
		per := round2(total / float64(spanDays))
		for k := 0; k < spanDays; k++ {
			out[d0.AddDate(0, 0, k)] = per
		}
	}
	return out
}

// rechargeEnergyBetween sums energy credited by recharges purchased in
// (after, until]. All times are UTC.
func rechargeEnergyBetween(rs []Recharge, after, until time.Time) float64 {
	var sum float64
	for _, r := range rs {
		if r.PurchasedAt.After(after) && !r.PurchasedAt.After(until) {
			sum += r.EnergyAmount
		}
	}
	return sum
}

// RenderMonthlyPNG draws daily spending for the current calendar month in
// `tz`. The x-axis always spans the full month even when there's little
// or no data. Days without a valid spending value (missing reading,
// stale cache read, or insufficient adjacency) leave a gap — no dot, no line.
//
// Spending for day D = balance(D) - balance(D+1) + energy credited by any
// recharges in that interval (see dailySpending), so:
//   - The very latest day in history never has spending yet (no D+1 reading)
//   - Recharge days show real consumption instead of a gap
//   - Cache-stale duplicate reads collapse to an honest gap, not a spike
func RenderMonthlyPNG(points []HistoryPoint, recharges []Recharge, tz *time.Location) ([]byte, error) {
	now := time.Now().In(tz)
	monthStart, monthEnd, daysInMonth := currentMonthBounds(now, tz)

	type pt struct {
		At    time.Time
		Spent float64
	}
	perDay := dailySpending(points, recharges, tz)

	// Walk every day of the month, building contiguous segments of data.
	// A "gap" day breaks the segment so the rendered line will too.
	var segments [][]pt
	var current []pt
	for d := monthStart; !d.After(monthEnd); d = d.AddDate(0, 0, 1) {
		anchor := noon(d, tz)
		if v, ok := perDay[d]; ok {
			current = append(current, pt{At: anchor, Spent: v})
		} else if len(current) > 0 {
			segments = append(segments, current)
			current = nil
		}
	}
	if len(current) > 0 {
		segments = append(segments, current)
	}

	// Totals + average computed over days WITH data only — averaging across
	// unmeasured days would understate true daily cost.
	var total float64
	var measuredDays int
	for _, seg := range segments {
		for _, p := range seg {
			total += p.Spent
			measuredDays++
		}
	}
	var avg float64
	if measuredDays > 0 {
		avg = total / float64(measuredDays)
	}

	leftAnchor := noon(monthStart, tz)
	rightAnchor := noon(monthEnd, tz)

	// Always present: invisible phantom series to anchor the x-range even
	// when `segments` is empty. Stroke and dot widths are 0 so it doesn't
	// render visually.
	series := []chart.Series{
		chart.TimeSeries{
			Style:   chart.Style{StrokeWidth: 0, DotWidth: 0},
			XValues: []time.Time{leftAnchor, rightAnchor},
			YValues: []float64{0, 0},
		},
	}

	// One TimeSeries per contiguous run. Single-point segments render as
	// just a dot (go-chart can't draw a line through 1 point); multi-point
	// segments render as line + dots.
	for _, seg := range segments {
		xs := make([]time.Time, len(seg))
		ys := make([]float64, len(seg))
		for i, p := range seg {
			xs[i] = p.At
			ys[i] = p.Spent
		}
		series = append(series, chart.TimeSeries{
			Style: chart.Style{
				StrokeColor: lineColor,
				StrokeWidth: 2.5,
				DotColor:    lineColor,
				DotWidth:    4.0,
			},
			XValues: xs,
			YValues: ys,
		})
	}

	// Avg line — only meaningful with ≥2 measured days.
	if measuredDays >= 2 && avg > 0 {
		series = append(series, chart.TimeSeries{
			Style: chart.Style{
				StrokeColor:     avgColor,
				StrokeWidth:     1.5,
				StrokeDashArray: []float64{6, 4},
			},
			XValues: []time.Time{leftAnchor, rightAnchor},
			YValues: []float64{avg, avg},
		})
	}

	title := buildMonthTitle(now, measuredDays, daysInMonth, avg, total)

	var maxObserved float64
	for _, seg := range segments {
		for _, p := range seg {
			if p.Spent > maxObserved {
				maxObserved = p.Spent
			}
		}
	}

	return renderChart(title, series, dayTicks(monthStart, monthEnd, tz),
		paddedYRange(maxObserved, avg),
		xRange(leftAnchor, rightAnchor))
}

func currentMonthBounds(now time.Time, tz *time.Location) (start, end time.Time, daysInMonth int) {
	start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz)
	// Last day of month = day 0 of next month
	next := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, tz)
	end = next.AddDate(0, 0, -1)
	daysInMonth = end.Day()
	return
}

func dayKey(t time.Time, tz *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, tz)
}

func noon(d time.Time, tz *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, tz)
}

// dayTicks returns one tick per local day in [first..last], anchored at
// noon (matching the data points). Labels show day-of-month ("02"); on
// the 1st the label expands to "Jan 2" so month boundaries are obvious.
func dayTicks(first, last time.Time, tz *time.Location) []chart.Tick {
	first = first.In(tz)
	last = last.In(tz)
	start := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, tz)
	end := time.Date(last.Year(), last.Month(), last.Day(), 0, 0, 0, 0, tz)
	if end.Before(last) {
		end = end.AddDate(0, 0, 1)
	}
	var ticks []chart.Tick
	for t := start; !t.After(end); t = t.AddDate(0, 0, 1) {
		anchor := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, tz)
		label := t.Format("02")
		if t.Day() == 1 {
			label = t.Format("Jan 2")
		}
		ticks = append(ticks, chart.Tick{
			Value: chart.TimeToFloat64(anchor),
			Label: label,
		})
	}
	return ticks
}

func buildMonthTitle(now time.Time, measuredDays, daysInMonth int, avg, total float64) string {
	month := now.Format("January 2006")
	if measuredDays == 0 {
		return fmt.Sprintf("Daily spending — %s  •  no completed-day data yet", month)
	}
	return fmt.Sprintf(
		"Daily spending — %s  •  %d/%d days with data  •  avg %.0f BDT/day  •  total %.0f BDT",
		month, measuredDays, daysInMonth, avg, total,
	)
}

func renderChart(title string, series []chart.Series, xticks []chart.Tick, yRange, xRange chart.Range) ([]byte, error) {
	graph := chart.Chart{
		Title:      title,
		TitleStyle: chart.Style{FontSize: 13, FontColor: textColor},
		Background: chart.Style{
			Padding:   chart.Box{Top: 40, Left: 30, Right: 30, Bottom: 40},
			FillColor: bgColor,
		},
		Canvas: chart.Style{FillColor: bgColor},
		XAxis: chart.XAxis{
			Ticks: xticks,
			Range: xRange,
			Style: chart.Style{
				FontColor:   textColor,
				StrokeColor: axisColor,
				FontSize:    9,
			},
			GridMajorStyle: chart.Style{
				StrokeColor:     gridColor,
				StrokeWidth:     1.0,
				StrokeDashArray: []float64{2, 4},
			},
		},
		YAxis: chart.YAxis{
			// go-chart puts "primary" Y axis on the RIGHT; secondary is on
			// the LEFT, which is the conventional placement.
			AxisType: chart.YAxisSecondary,
			Range:    yRange,
			ValueFormatter: func(v any) string {
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%.0f", f)
				}
				return ""
			},
			Style: chart.Style{
				FontColor:   textColor,
				StrokeColor: axisColor,
				FontSize:    10,
			},
			GridMajorStyle: chart.Style{
				StrokeColor:     gridColor,
				StrokeWidth:     1.0,
				StrokeDashArray: []float64{2, 4},
			},
		},
		Series: series,
		Width:  960,
		Height: 420,
	}

	var buf bytes.Buffer
	if err := graph.Render(chart.PNG, &buf); err != nil {
		return nil, fmt.Errorf("render png: %w", err)
	}
	return buf.Bytes(), nil
}

// xRange returns a chart.Range covering exactly [first..last] so the
// axis always displays the full intended span regardless of data coverage.
func xRange(first, last time.Time) chart.Range {
	return &chart.ContinuousRange{
		Min: chart.TimeToFloat64(first),
		Max: chart.TimeToFloat64(last),
	}
}

// paddedYRange gives ~10% headroom above the max and always anchors at 0.
// `maxObserved` may be 0 (no data) in which case we use a placeholder so
// the empty axis still renders a sensible scale.
func paddedYRange(maxObserved, avg float64) chart.Range {
	max := maxObserved
	if avg > max {
		max = avg
	}
	if max == 0 {
		max = 100
	}
	return &chart.ContinuousRange{Min: 0, Max: max * 1.10}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
