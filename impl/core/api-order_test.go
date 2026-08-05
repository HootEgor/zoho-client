package core

import "testing"

// TestTotalsDiverged pins the tolerance against the case that motivated it: order 17134, where
// Zoho's subform was repriced from the 8.40% discount we sent to a flat 10% while every product
// and quantity stayed put. The items looked unchanged, so the totals were left alone and the two
// systems drifted 89 PLN apart in silence.
func TestTotalsDiverged(t *testing.T) {
	tests := []struct {
		name     string
		ocTotal  float64
		zohoTot  float64
		wantDiff float64
		want     bool
	}{
		{
			// The real divergence: Zoho reapplied the 10% at 19% VAT on the discounted base.
			name:     "order 17134 repriced in zoho",
			ocTotal:  5124.11,
			zohoTot:  5035.09,
			wantDiff: -89.02,
			want:     true,
		},
		{
			// The same order the day it synced: Zoho's own per-line rounding over 133 units.
			name:    "rounding drift on a long subform",
			ocTotal: 5124.11,
			zohoTot: 5123.93,
			want:    false,
		},
		{
			name:    "identical totals",
			ocTotal: 5124.11,
			zohoTot: 5124.11,
			want:    false,
		},
		{
			// Small order: the floor, not the rate, decides. 0.1% of 80 is 8 groszy.
			name:    "small order below the floor",
			ocTotal: 80.00,
			zohoTot: 80.90,
			want:    false,
		},
		{
			name:     "small order above the floor",
			ocTotal:  80.00,
			zohoTot:  78.50,
			wantDiff: -1.50,
			want:     true,
		},
		{
			// Zoho gaining money is as much a divergence as Zoho losing it.
			name:     "zoho higher than opencart",
			ocTotal:  5124.11,
			zohoTot:  5300.00,
			wantDiff: 175.89,
			want:     true,
		},
		{
			name:    "payload carries no grand total",
			ocTotal: 5124.11,
			zohoTot: 0,
			want:    false,
		},
		{
			name:    "opencart total missing",
			ocTotal: 0,
			zohoTot: 5035.09,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, got := totalsDiverged(tt.ocTotal, tt.zohoTot)
			if got != tt.want {
				t.Errorf("totalsDiverged(%.2f, %.2f) = %v, want %v (diff %.2f)",
					tt.ocTotal, tt.zohoTot, got, tt.want, diff)
			}
			if tt.want && !approx(diff, tt.wantDiff, 0.005) {
				t.Errorf("diff = %.2f, want %.2f", diff, tt.wantDiff)
			}
		})
	}
}
