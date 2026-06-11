package sync

import "testing"

// TestShouldChargeFailure covers the decision boundary between persisting a
// body-parse failure (body_failed=1, message permanently skipped) and
// deferring to a future sync cycle when the response looks truncated.
//
// All sizes in bytes. maxMessageSize is the production constant — picking
// inputs around it directly verifies the "Aerion-side cap is not a server
// truncation" branch.
func TestShouldChargeFailure(t *testing.T) {
	cases := []struct {
		name         string
		received     int64
		reported     int64
		wantedCharge bool
	}{
		{
			name:         "no reported size: charge (no signal to defer on)",
			received:     5_000,
			reported:     0,
			wantedCharge: true,
		},
		{
			name:         "received hit Aerion's cap: charge (intentional truncation, retry won't help)",
			received:     maxMessageSize,
			reported:     maxMessageSize + 50_000_000,
			wantedCharge: true,
		},
		{
			name:         "received well below threshold: DEFER (likely server-side truncation)",
			received:     1_000,
			reported:     50_000,
			wantedCharge: false,
		},
		{
			name:         "received exactly at threshold (80%): charge (close enough; treat as real)",
			received:     8_000,
			reported:     10_000,
			wantedCharge: true,
		},
		{
			name:         "received just under threshold: DEFER",
			received:     7_999,
			reported:     10_000,
			wantedCharge: false,
		},
		{
			name:         "received equals reported: charge (full payload, empty parse is real)",
			received:     12_000,
			reported:     12_000,
			wantedCharge: true,
		},
		{
			name:         "received slightly over reported: charge (framing variation)",
			received:     12_010,
			reported:     12_000,
			wantedCharge: true,
		},
		{
			name:         "tiny message both received and reported: charge",
			received:     50,
			reported:     50,
			wantedCharge: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldChargeFailure(tc.received, tc.reported)
			if got != tc.wantedCharge {
				t.Errorf("shouldChargeFailure(received=%d, reported=%d) = %v, want %v",
					tc.received, tc.reported, got, tc.wantedCharge)
			}
		})
	}
}
