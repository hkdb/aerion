package backend

import "strings"

// ICS↔Google PARTSTAT translation. Google's enum
// (https://developers.google.com/workspace/calendar/api/v3/reference/events):
//   needsAction | declined | tentative | accepted
//
// ICS (RFC 5545 §3.2.12):
//   NEEDS-ACTION | ACCEPTED | DECLINED | TENTATIVE | DELEGATED | COMPLETED | IN-PROCESS
//
// DELEGATED maps to needsAction on the Google side (Google doesn't model
// delegation; the delegated user receives the invitation per Google's
// internal handling). Unknown values default to needsAction.

func icsPartStatToGoogle(ps string) string {
	switch strings.ToUpper(strings.TrimSpace(ps)) {
	case PartStatAccepted:
		return "accepted"
	case PartStatDeclined:
		return "declined"
	case PartStatTentative:
		return "tentative"
	case PartStatNeedsAction, PartStatDelegated, "":
		return "needsAction"
	}
	return "needsAction"
}

func googlePartStatToICS(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "accepted":
		return PartStatAccepted
	case "declined":
		return PartStatDeclined
	case "tentative":
		return PartStatTentative
	case "needsaction", "":
		return PartStatNeedsAction
	}
	return PartStatNeedsAction
}

// ICS↔Microsoft Graph PARTSTAT translation. Graph's enum
// (https://learn.microsoft.com/en-us/graph/api/resources/responsestatus):
//   none | organizer | tentativelyAccepted | accepted | declined | notResponded
//
// On reads: `organizer` collapses to `ACCEPTED` (organizer implicitly
// attends), `notResponded` to `NEEDS-ACTION`, `none` to empty (caller
// should treat as default — we map to NEEDS-ACTION).
//
// On writes: ICS `DELEGATED` collapses to `notResponded` (closest match;
// Graph has no first-class delegation status).

func icsPartStatToGraph(ps string) string {
	switch strings.ToUpper(strings.TrimSpace(ps)) {
	case PartStatAccepted:
		return "accepted"
	case PartStatDeclined:
		return "declined"
	case PartStatTentative:
		return "tentativelyAccepted"
	case PartStatNeedsAction, "":
		return "notResponded"
	case PartStatDelegated:
		return "notResponded"
	}
	return "notResponded"
}

func graphPartStatToICS(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "accepted", "organizer":
		return PartStatAccepted
	case "declined":
		return PartStatDeclined
	case "tentativelyaccepted":
		return PartStatTentative
	case "notresponded", "none", "":
		return PartStatNeedsAction
	}
	return PartStatNeedsAction
}
