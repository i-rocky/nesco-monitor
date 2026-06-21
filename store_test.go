package main

import (
	"context"
	"testing"
	"time"

	_ "time/tzdata"
)

func TestUpsertRechargesIdempotent(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	rs := []Recharge{
		{OrderID: "A", Token: "t1", EnergyAmount: 1914.24, PaidAmount: 2000, PurchasedAt: time.Date(2026, 6, 21, 7, 17, 0, 0, time.UTC)},
		{OrderID: "B", Token: "t2", EnergyAmount: 478.56, PaidAmount: 500, PurchasedAt: time.Date(2026, 6, 18, 19, 13, 0, 0, time.UTC)},
	}
	if err := s.UpsertRecharges(ctx, "12345678", rs); err != nil {
		t.Fatal(err)
	}
	// Upserting the same rows again must not duplicate.
	if err := s.UpsertRecharges(ctx, "12345678", rs); err != nil {
		t.Fatal(err)
	}

	got, err := s.RechargesSince(ctx, "12345678", 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("recharges = %d, want 2 (dedup by order id failed)", len(got))
	}
	// Ordered ascending by purchased_at: B (Jun 18) before A (Jun 21).
	if got[0].OrderID != "B" || got[1].OrderID != "A" {
		t.Errorf("order = %s,%s want B,A", got[0].OrderID, got[1].OrderID)
	}
	if got[1].EnergyAmount != 1914.24 {
		t.Errorf("energy roundtrip = %v", got[1].EnergyAmount)
	}
}

func TestPreviousReading(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	t1 := time.Date(2026, 6, 19, 23, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 20, 23, 0, 0, 0, time.UTC)
	for _, snap := range []*BalanceSnapshot{
		{AccountNo: "12345678", Balance: 98.88, FetchedAt: t1},
		{AccountNo: "12345678", Balance: -253.67, FetchedAt: t2},
	} {
		if err := s.Insert(ctx, snap); err != nil {
			t.Fatal(err)
		}
	}

	bal, at, ok, err := s.PreviousReading(ctx, "12345678", t2)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if bal != 98.88 {
		t.Errorf("prev balance = %v, want 98.88", bal)
	}
	if !at.Equal(t1) {
		t.Errorf("prev time = %s, want %s", at, t1)
	}
}
