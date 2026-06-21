package main

import "testing"

func TestShouldStoreReading(t *testing.T) {
	cases := []struct {
		name    string
		hasPrev bool
		prev    float64
		cur     float64
		want    bool
	}{
		{"first reading always stored", false, 0, -253.67, true},
		{"unchanged balance skipped (cache/restart dup)", true, -253.67, -253.67, false},
		{"changed balance stored", true, 98.88, -253.67, true},
		{"recharge jump stored", true, 346.34, 3635.54, true},
	}
	for _, c := range cases {
		if got := shouldStoreReading(c.hasPrev, c.prev, c.cur); got != c.want {
			t.Errorf("%s: shouldStoreReading(%v,%v,%v)=%v want %v",
				c.name, c.hasPrev, c.prev, c.cur, got, c.want)
		}
	}
}
