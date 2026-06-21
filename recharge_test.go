package main

import (
	"os"
	"testing"
	"time"

	_ "time/tzdata"
)

func TestParseBalancePage_Recharges(t *testing.T) {
	f, err := os.Open("testdata/panel_sample.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	snap, err := parseBalancePage(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if snap.Balance != -253.67 {
		t.Errorf("balance = %v, want -253.67", snap.Balance)
	}
	if len(snap.Recharges) != 2 {
		t.Fatalf("recharges = %d, want 2", len(snap.Recharges))
	}

	r := snap.Recharges[0]
	if r.OrderID != "1000000000000000001" {
		t.Errorf("order id = %q", r.OrderID)
	}
	if r.EnergyAmount != 1914.24 {
		t.Errorf("energy = %v, want 1914.24", r.EnergyAmount)
	}
	if r.PaidAmount != 2000 {
		t.Errorf("paid = %v, want 2000", r.PaidAmount)
	}
	dhaka, _ := time.LoadLocation("Asia/Dhaka")
	want := time.Date(2026, 6, 21, 13, 17, 0, 0, dhaka)
	if !r.PurchasedAt.Equal(want) {
		t.Errorf("purchased_at = %s, want %s", r.PurchasedAt, want)
	}
}
