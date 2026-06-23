package backend

// PartStat is the iCalendar PARTSTAT enum (RFC 5545 § 3.2.12). Stored on
// Attendee. The Aerion in-memory enum mirrors the wire-format strings so
// translation to/from ICS is identity; provider-specific translation (Google
// "needsAction|declined|tentative|accepted", Graph "none|organizer|
// tentativelyAccepted|accepted|declined|notResponded") happens in
// partstat_map.go.
const (
	PartStatNeedsAction = "NEEDS-ACTION"
	PartStatAccepted    = "ACCEPTED"
	PartStatDeclined    = "DECLINED"
	PartStatTentative   = "TENTATIVE"
	PartStatDelegated   = "DELEGATED"
)

// Role is the iCalendar ROLE param (RFC 5545 § 3.2.18). Default
// REQ-PARTICIPANT when ATTENDEE has no ROLE.
const (
	RoleChair          = "CHAIR"
	RoleReqParticipant = "REQ-PARTICIPANT"
	RoleOptParticipant = "OPT-PARTICIPANT"
	RoleNonParticipant = "NON-PARTICIPANT"
)

// CUType is the iCalendar CUTYPE param (calendar-user-type, RFC 5545 §
// 3.2.3). Default INDIVIDUAL when CUTYPE absent.
const (
	CUTypeIndividual = "INDIVIDUAL"
	CUTypeGroup      = "GROUP"
	CUTypeResource   = "RESOURCE"
	CUTypeRoom       = "ROOM"
	CUTypeUnknown    = "UNKNOWN"
)

// Attendee is one ATTENDEE row on a calendar event. Lowercased Email is the
// stable identity (used for self-match against Account/Identity emails);
// CommonName is the display label as seen on the wire (preserve casing).
//
// Stored as a JSON array on `events.attendees_json` and rebuilt from there
// into the `event_attendee_index` side table on every UpsertEventTx.
type Attendee struct {
	Email      string `json:"email"`
	CommonName string `json:"cn,omitempty"`
	PartStat   string `json:"partStat,omitempty"`   // PartStat* const
	Role       string `json:"role,omitempty"`       // Role* const
	RSVP       bool   `json:"rsvp,omitempty"`       // RSVP param TRUE/FALSE
	CUType     string `json:"cuType,omitempty"`     // CUType* const
	Delegate   string `json:"delegate,omitempty"`   // DELEGATED-TO email, lowercase

	// ScheduleStatus is RFC 6638's SCHEDULE-STATUS param, returned by
	// CalDAV servers after a PUT to indicate iTIP delivery state.
	// Codes (subset): "1.0"=pending, "1.1"=sent, "1.2"=delivered,
	// "3.7"=unrecognized user, "5.1"=unspecified error. Surfaced on the
	// detail-pane attendee row when non-empty. Empty for Google/Microsoft
	// (those providers don't return iTIP delivery codes).
	ScheduleStatus string `json:"scheduleStatus,omitempty"`
}

// Organizer is the event's ORGANIZER. Optional — local calendars without
// scheduling intent may omit. Identity of "self as organizer" is determined
// by matching Email against the union of all Account.Email + Identity.Email.
type Organizer struct {
	Email      string `json:"email"`
	CommonName string `json:"cn,omitempty"`
}

// AttendeeInput is the Wails-bound write-side type used in EventInput.
// Mirrors Attendee but omits server-derived fields (ScheduleStatus).
type AttendeeInput struct {
	Email      string `json:"email"`
	CommonName string `json:"cn,omitempty"`
	PartStat   string `json:"partStat,omitempty"`
	Role       string `json:"role,omitempty"`
	RSVP       bool   `json:"rsvp,omitempty"`
	CUType     string `json:"cuType,omitempty"`
	Delegate   string `json:"delegate,omitempty"`
}

// OrganizerInput is the Wails-bound write-side type used in EventInput.
type OrganizerInput struct {
	Email      string `json:"email"`
	CommonName string `json:"cn,omitempty"`
}
