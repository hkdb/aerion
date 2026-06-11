package backend

// Tests for the CalDAV VCALENDAR composition helper. composeVCalendar
// rewrites the master resource's body to apply a per-instance edit; the
// HTTP PUT plumbing lives in PushInstance + caldavPut and is exercised
// by the httptest-based provider tests separately.

import (
	"strings"
	"testing"
	"time"
)

// buildSeedMasterICS constructs an ICS blob containing a master VEVENT
// (with the given RRULE) plus zero or more override VEVENTs. Used as
// the input to composeVCalendar in the tests below.
func buildSeedMasterICS(t *testing.T, uid string, rrule string, instanceTimeUnix int64, overrides []int64) string {
	t.Helper()
	in := EventInput{
		Summary:     "Original",
		DTStartUnix: instanceTimeUnix,
		DTEndUnix:   instanceTimeUnix + 3600,
		Recurrence:  &RecurrenceSpec{Freq: rrule},
	}
	blob, err := serializeVEVENT(uid, in)
	if err != nil {
		t.Fatalf("serializeVEVENT: %v", err)
	}
	if len(overrides) == 0 {
		return blob
	}
	// Splice override VEVENTs into the calendar.
	cal, err := decodeVCalendar(blob)
	if err != nil {
		t.Fatalf("decode seed master: %v", err)
	}
	for _, ot := range overrides {
		ov := buildOverrideVEVENT(uid, ot, EventInput{
			Summary:     "Old override",
			DTStartUnix: ot,
			DTEndUnix:   ot + 1800,
		})
		cal.Children = append(cal.Children, ov.Component)
	}
	out, err := encodeICS(cal)
	if err != nil {
		t.Fatalf("encode seed master: %v", err)
	}
	return out
}

func TestComposeVCalendar_ThisUpdate_AppendsOverride(t *testing.T) {
	uid := "evt-x@aerion-caldav"
	masterStart := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).Unix()
	instanceTime := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Unix()

	masterBlob := buildSeedMasterICS(t, uid, "WEEKLY", masterStart, nil)
	master := Event{UID: uid, ICSBlob: masterBlob}

	composed, err := composeVCalendar(PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: instanceTime,
		Op:               EditScopeThis,
		Kind:             InstanceOpUpdate,
		In: EventInput{
			Summary:     "Modified instance",
			DTStartUnix: instanceTime,
			DTEndUnix:   instanceTime + 3600,
		},
	})
	if err != nil {
		t.Fatalf("composeVCalendar: %v", err)
	}
	if composed.NewSeries != nil {
		t.Errorf("NewSeries should be nil for scope=this")
	}
	if !strings.Contains(composed.MasterBlob, "SUMMARY:Modified instance") {
		t.Errorf("blob missing modified SUMMARY:\n%s", composed.MasterBlob)
	}
	if !strings.Contains(composed.MasterBlob, "RECURRENCE-ID:20260615T090000Z") {
		t.Errorf("blob missing RECURRENCE-ID:\n%s", composed.MasterBlob)
	}
	// Count VEVENT components — should be 2 (master + new override).
	if got := strings.Count(composed.MasterBlob, "BEGIN:VEVENT"); got != 2 {
		t.Errorf("VEVENT count = %d, want 2", got)
	}
}

func TestComposeVCalendar_ThisUpdate_ReplacesExistingOverride(t *testing.T) {
	uid := "evt-x@aerion-caldav"
	masterStart := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).Unix()
	instanceTime := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Unix()
	otherOverrideTime := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC).Unix()

	masterBlob := buildSeedMasterICS(t, uid, "WEEKLY", masterStart, []int64{instanceTime, otherOverrideTime})
	master := Event{UID: uid, ICSBlob: masterBlob}

	composed, err := composeVCalendar(PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: instanceTime,
		Op:               EditScopeThis,
		Kind:             InstanceOpUpdate,
		In: EventInput{
			Summary:     "New override text",
			DTStartUnix: instanceTime,
			DTEndUnix:   instanceTime + 3600,
		},
	})
	if err != nil {
		t.Fatalf("composeVCalendar: %v", err)
	}
	if got := strings.Count(composed.MasterBlob, "BEGIN:VEVENT"); got != 3 {
		t.Errorf("VEVENT count = %d, want 3 (master + 2 overrides)", got)
	}
	// The override matching the targeted time has the new SUMMARY; the
	// other override is unchanged.
	if !strings.Contains(composed.MasterBlob, "SUMMARY:New override text") {
		t.Errorf("blob missing new override SUMMARY")
	}
	if !strings.Contains(composed.MasterBlob, "SUMMARY:Old override") {
		t.Errorf("blob missing untouched override SUMMARY")
	}
}

func TestComposeVCalendar_ThisDelete_AddsEXDATE(t *testing.T) {
	uid := "evt-y@aerion-caldav"
	masterStart := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).Unix()
	instanceTime := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC).Unix()

	masterBlob := buildSeedMasterICS(t, uid, "WEEKLY", masterStart, nil)
	master := Event{UID: uid, ICSBlob: masterBlob}

	composed, err := composeVCalendar(PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: instanceTime,
		Op:               EditScopeThis,
		Kind:             InstanceOpDelete,
	})
	if err != nil {
		t.Fatalf("composeVCalendar: %v", err)
	}
	if !strings.Contains(composed.MasterBlob, "EXDATE:20260622T090000Z") {
		t.Errorf("blob missing EXDATE:\n%s", composed.MasterBlob)
	}
	if got := strings.Count(composed.MasterBlob, "BEGIN:VEVENT"); got != 1 {
		t.Errorf("VEVENT count = %d, want 1 (master only)", got)
	}
}

func TestComposeVCalendar_ThisDelete_DropsMatchingOverride(t *testing.T) {
	uid := "evt-z@aerion-caldav"
	masterStart := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).Unix()
	instanceTime := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).Unix()
	otherOverrideTime := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC).Unix()

	masterBlob := buildSeedMasterICS(t, uid, "WEEKLY", masterStart, []int64{instanceTime, otherOverrideTime})
	master := Event{UID: uid, ICSBlob: masterBlob}

	composed, err := composeVCalendar(PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: instanceTime,
		Op:               EditScopeThis,
		Kind:             InstanceOpDelete,
	})
	if err != nil {
		t.Fatalf("composeVCalendar: %v", err)
	}
	// Master + the OTHER override (the matching one was dropped).
	if got := strings.Count(composed.MasterBlob, "BEGIN:VEVENT"); got != 2 {
		t.Errorf("VEVENT count = %d, want 2 (master + untouched override)", got)
	}
	if !strings.Contains(composed.MasterBlob, "EXDATE:20260615T090000Z") {
		t.Errorf("blob missing EXDATE:\n%s", composed.MasterBlob)
	}
}

func TestComposeVCalendar_ThisAndFutureUpdate_ClampsMasterAndCreatesNewSeries(t *testing.T) {
	uid := "evt-tf@aerion-caldav"
	masterStart := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).Unix()
	splitTime := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC).Unix()
	pastOverrideTime := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC).Unix()
	futureOverrideTime := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC).Unix()

	masterBlob := buildSeedMasterICS(t, uid, "WEEKLY", masterStart, []int64{pastOverrideTime, futureOverrideTime})
	master := Event{UID: uid, RRuleText: "FREQ=WEEKLY", ICSBlob: masterBlob}

	composed, err := composeVCalendar(PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: splitTime,
		Op:               EditScopeThisAndFuture,
		Kind:             InstanceOpUpdate,
		In: EventInput{
			Summary:     "New series title",
			DTStartUnix: splitTime,
			DTEndUnix:   splitTime + 3600,
			Recurrence:  &RecurrenceSpec{Freq: "WEEKLY"},
		},
	})
	if err != nil {
		t.Fatalf("composeVCalendar: %v", err)
	}

	// Master: clamped RRULE + past override kept + future override dropped.
	if !strings.Contains(composed.MasterBlob, "UNTIL=") {
		t.Errorf("master missing UNTIL clamp:\n%s", composed.MasterBlob)
	}
	if got := strings.Count(composed.MasterBlob, "BEGIN:VEVENT"); got != 2 {
		t.Errorf("master VEVENT count = %d, want 2 (master + past override)", got)
	}
	if !strings.Contains(composed.MasterBlob, "SUMMARY:Old override") {
		t.Errorf("master missing the past override SUMMARY")
	}

	// New series: separate VCALENDAR with the new fields + new UID.
	if composed.NewSeries == nil {
		t.Fatalf("NewSeries should be set for this-and-future + update")
	}
	if composed.NewSeries.UID == "" || composed.NewSeries.UID == uid {
		t.Errorf("NewSeries UID should be fresh; got %q", composed.NewSeries.UID)
	}
	if !strings.Contains(composed.NewSeries.ICSBlob, "SUMMARY:New series title") {
		t.Errorf("new series blob missing SUMMARY")
	}
	if !strings.Contains(composed.NewSeries.ICSBlob, "FREQ=WEEKLY") {
		t.Errorf("new series blob missing RRULE")
	}
}

func TestComposeVCalendar_ThisAndFutureDelete_ClampsMasterOnly(t *testing.T) {
	uid := "evt-tfd@aerion-caldav"
	masterStart := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).Unix()
	splitTime := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC).Unix()

	masterBlob := buildSeedMasterICS(t, uid, "WEEKLY", masterStart, nil)
	master := Event{UID: uid, RRuleText: "FREQ=WEEKLY", ICSBlob: masterBlob}

	composed, err := composeVCalendar(PushInstancePayload{
		Master:           master,
		InstanceTimeUnix: splitTime,
		Op:               EditScopeThisAndFuture,
		Kind:             InstanceOpDelete,
	})
	if err != nil {
		t.Fatalf("composeVCalendar: %v", err)
	}
	if composed.NewSeries != nil {
		t.Errorf("NewSeries should be nil for delete")
	}
	if !strings.Contains(composed.MasterBlob, "UNTIL=") {
		t.Errorf("master missing UNTIL clamp")
	}
}

func TestRecurrenceIDMatches_RoundTrip(t *testing.T) {
	instanceTime := time.Date(2026, 7, 4, 14, 30, 0, 0, time.UTC).Unix()
	in := EventInput{
		Summary:     "Test",
		DTStartUnix: instanceTime,
		DTEndUnix:   instanceTime + 1800,
	}
	ev := buildOverrideVEVENT("uid@a", instanceTime, in)
	ridProp := ev.Props.Get("RECURRENCE-ID")
	if ridProp == nil {
		t.Fatalf("override missing RECURRENCE-ID")
	}
	if !recurrenceIDMatches(ridProp, instanceTime) {
		t.Errorf("recurrenceIDMatches should match the originating time")
	}
	if recurrenceIDMatches(ridProp, instanceTime+1) {
		t.Errorf("recurrenceIDMatches should NOT match a different time")
	}
}
