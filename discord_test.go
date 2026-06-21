package main

import (
	"testing"
	"time"

	_ "time/tzdata"
)

func TestPendingCredit(t *testing.T) {
	prevTime := time.Date(2026, 6, 20, 23, 0, 0, 0, time.UTC) // Jun 21 05:00 Dhaka
	// Recharge purchased after the last reading; balance has NOT jumped up
	// (still -253.67 vs prev 98.88) -> credit is pending.
	snap := &BalanceSnapshot{
		Balance:   -253.67,
		FetchedAt: prevTime.Add(8 * time.Hour),
		Recharges: []Recharge{
			{OrderID: "x", EnergyAmount: 1914.24, PurchasedAt: prevTime.Add(6 * time.Hour)},
			{OrderID: "old", EnergyAmount: 478.56, PurchasedAt: prevTime.Add(-48 * time.Hour)},
		},
	}
	got := pendingCredit(snap, 98.88, prevTime, true)
	if got != 1914.24 {
		t.Errorf("pendingCredit = %v, want 1914.24 (only the post-reading, unreflected recharge)", got)
	}

	// If balance jumped up (credit already reflected), nothing is pending.
	snap.Balance = 1660.0
	if got := pendingCredit(snap, 98.88, prevTime, true); got != 0 {
		t.Errorf("pendingCredit = %v, want 0 when balance already rose", got)
	}
}
