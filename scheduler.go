package main

import (
	"fmt"
	"time"
)

// pollTimeString renders the daily poll time for logs, e.g. "05:00 Asia/Dhaka".
func pollTimeString() string {
	return fmt.Sprintf("%02d:%02d Asia/Dhaka", pollHour, pollMinute)
}

// pollHour and pollMinute set when the daily poll fires in Asia/Dhaka.
// NESCO updates the balance at 00:00 local but sometimes serves a cached
// previous-day value for the first hour+. Polling at 05:00 — five hours
// later — reads the freshly-updated value after the cache has invalidated.
const (
	pollHour   = 5
	pollMinute = 0
)

// NextTick returns the next 01:10 Asia/Dhaka strictly after `now`.
func NextTick(now time.Time, tz *time.Location) time.Time {
	local := now.In(tz)
	candidate := time.Date(local.Year(), local.Month(), local.Day(),
		pollHour, pollMinute, 0, 0, tz)
	if !candidate.After(local) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}
