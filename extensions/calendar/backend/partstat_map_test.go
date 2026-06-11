package backend

import "testing"

func TestPartStatMap_GoogleRoundTrip(t *testing.T) {
	tests := []struct {
		ics    string
		google string
	}{
		{PartStatAccepted, "accepted"},
		{PartStatDeclined, "declined"},
		{PartStatTentative, "tentative"},
		{PartStatNeedsAction, "needsAction"},
		{PartStatDelegated, "needsAction"}, // Google has no DELEGATED
		{"", "needsAction"},
		{"BOGUS", "needsAction"},
	}
	for _, tc := range tests {
		if got := icsPartStatToGoogle(tc.ics); got != tc.google {
			t.Errorf("icsPartStatToGoogle(%q) = %q, want %q", tc.ics, got, tc.google)
		}
	}
	// Reverse — only canonical Google values produce ICS round-trip.
	reverseCanonical := map[string]string{
		"accepted":    PartStatAccepted,
		"declined":    PartStatDeclined,
		"tentative":   PartStatTentative,
		"needsAction": PartStatNeedsAction,
		"":            PartStatNeedsAction,
	}
	for in, want := range reverseCanonical {
		if got := googlePartStatToICS(in); got != want {
			t.Errorf("googlePartStatToICS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPartStatMap_GraphRoundTrip(t *testing.T) {
	tests := []struct {
		ics   string
		graph string
	}{
		{PartStatAccepted, "accepted"},
		{PartStatDeclined, "declined"},
		{PartStatTentative, "tentativelyAccepted"},
		{PartStatNeedsAction, "notResponded"},
		{PartStatDelegated, "notResponded"}, // Graph has no DELEGATED
		{"", "notResponded"},
	}
	for _, tc := range tests {
		if got := icsPartStatToGraph(tc.ics); got != tc.graph {
			t.Errorf("icsPartStatToGraph(%q) = %q, want %q", tc.ics, got, tc.graph)
		}
	}
	// Reverse — including Graph's `organizer` collapsing to ACCEPTED.
	reverse := map[string]string{
		"accepted":            PartStatAccepted,
		"organizer":           PartStatAccepted, // collapse
		"declined":            PartStatDeclined,
		"tentativelyAccepted": PartStatTentative,
		"notResponded":        PartStatNeedsAction,
		"none":                PartStatNeedsAction, // default
		"":                    PartStatNeedsAction,
	}
	for in, want := range reverse {
		if got := graphPartStatToICS(in); got != want {
			t.Errorf("graphPartStatToICS(%q) = %q, want %q", in, got, want)
		}
	}
}
