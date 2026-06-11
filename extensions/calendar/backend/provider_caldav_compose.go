package backend

// CalDAV VCALENDAR composition for per-instance edits.
//
// CalDAV stores recurring events as a single resource whose body is a
// VCALENDAR containing the master VEVENT plus zero-or-more "override"
// VEVENTs (each carrying a RECURRENCE-ID property pointing at the
// occurrence it replaces). When the user edits or deletes a single
// instance (or the "this and following" range), Chunk 7's PushInstance
// rewrites that single resource — overrides aren't separate HTTP
// resources on the CalDAV side.
//
// composeVCalendar produces the new VCALENDAR blob (and, for the
// this-and-future + update case, a second blob for the new series being
// created at a fresh resource URL). The caller PUTs the master blob to
// master.Href and, if a NewSeries is produced, PUTs that blob to a new
// {cal.URL}/{newUID}.ics.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/google/uuid"

	"github.com/hkdb/aerion/internal/kit/davutil"
)

// composedVCalendar is the output of composeVCalendar. NewSeries is
// non-nil only for scope=ThisAndFuture + update.
type composedVCalendar struct {
	MasterBlob string // ICS to PUT to master.Href
	NewSeries  *composedNewSeries
}

type composedNewSeries struct {
	UID     string // new master UID (for events row)
	ICSBlob string // ICS to PUT to a fresh resource URL
}

// composeVCalendar rewrites a CalDAV VCALENDAR resource to apply a
// per-instance edit. Switches on (Op, Kind):
//   - this   + update → insert/replace override VEVENT carrying RECURRENCE-ID.
//   - this   + delete → drop matching override + add EXDATE on master.
//   - future + update → clamp master's RRULE UNTIL + drop future overrides;
//     plus a brand-new VCALENDAR for the new series.
//   - future + delete → clamp + drop future overrides only.
func composeVCalendar(payload PushInstancePayload) (composedVCalendar, error) {
	cal, err := decodeVCalendar(payload.Master.ICSBlob)
	if err != nil {
		return composedVCalendar{}, err
	}

	switch payload.Op {
	case EditScopeThis:
		return composeThis(cal, payload)
	case EditScopeThisAndFuture:
		return composeThisAndFuture(cal, payload)
	}
	return composedVCalendar{}, fmt.Errorf("composeVCalendar: unsupported scope %q", payload.Op)
}

// composeThis handles scope=this — modify or remove a single occurrence.
func composeThis(cal *ical.Calendar, payload PushInstancePayload) (composedVCalendar, error) {
	masterIdx, overrides := classifyVEvents(cal)
	if masterIdx < 0 {
		return composedVCalendar{}, fmt.Errorf("composeVCalendar: no master VEVENT in calendar")
	}

	switch payload.Kind {
	case InstanceOpUpdate:
		return composeThisUpdate(cal, payload, masterIdx, overrides)
	case InstanceOpDelete:
		return composeThisDelete(cal, payload, masterIdx, overrides)
	}
	return composedVCalendar{}, fmt.Errorf("composeVCalendar: unsupported kind %q", payload.Kind)
}

func composeThisUpdate(cal *ical.Calendar, payload PushInstancePayload, masterIdx int, overrides []int) (composedVCalendar, error) {
	masterUID := payload.Master.UID

	// Build the new override VEVENT carrying RECURRENCE-ID.
	overrideEv := buildOverrideVEVENT(masterUID, payload.InstanceTimeUnix, payload.In)

	// Find an existing override with matching RECURRENCE-ID — replace it
	// in place. Otherwise append.
	replaced := false
	for _, idx := range overrides {
		child := cal.Children[idx]
		ridProp := child.Props.Get(ical.PropRecurrenceID)
		if ridProp == nil {
			continue
		}
		if recurrenceIDMatches(ridProp, payload.InstanceTimeUnix) {
			cal.Children[idx] = overrideEv.Component
			replaced = true
			break
		}
	}
	if !replaced {
		cal.Children = append(cal.Children, overrideEv.Component)
	}

	blob, err := encodeICS(cal)
	if err != nil {
		return composedVCalendar{}, fmt.Errorf("encode updated calendar: %w", err)
	}
	return composedVCalendar{MasterBlob: blob}, nil
}

func composeThisDelete(cal *ical.Calendar, payload PushInstancePayload, masterIdx int, overrides []int) (composedVCalendar, error) {
	// Drop any matching override VEVENT.
	cal.Children = dropMatchingOverrides(cal.Children, overrides, payload.InstanceTimeUnix)

	// Add EXDATE to master.
	master := cal.Children[reindexMaster(cal, masterIdx)]
	addExdateProp(master.Props, payload.InstanceTimeUnix)

	blob, err := encodeICS(cal)
	if err != nil {
		return composedVCalendar{}, fmt.Errorf("encode updated calendar: %w", err)
	}
	return composedVCalendar{MasterBlob: blob}, nil
}

// composeThisAndFuture clamps the master's RRULE UNTIL to the split point
// and (for update) creates a new VCALENDAR for the new series.
func composeThisAndFuture(cal *ical.Calendar, payload PushInstancePayload) (composedVCalendar, error) {
	masterIdx, overrides := classifyVEvents(cal)
	if masterIdx < 0 {
		return composedVCalendar{}, fmt.Errorf("composeVCalendar: no master VEVENT in calendar")
	}
	splitUnix := payload.InstanceTimeUnix

	// Clamp master RRULE.
	masterComp := cal.Children[masterIdx]
	rruleProp := masterComp.Props.Get(ical.PropRecurrenceRule)
	if rruleProp == nil {
		return composedVCalendar{}, fmt.Errorf("composeVCalendar: master has no RRULE")
	}
	clampedRRULE := clampRRuleUntil(rruleProp.Value, splitUnix-1)
	setRRuleText(masterComp.Props, clampedRRULE)

	// Drop overrides at or past the split point.
	cal.Children = dropFutureOverrides(cal.Children, overrides, splitUnix)

	masterBlob, err := encodeICS(cal)
	if err != nil {
		return composedVCalendar{}, fmt.Errorf("encode clamped master: %w", err)
	}

	out := composedVCalendar{MasterBlob: masterBlob}

	if payload.Kind == InstanceOpUpdate {
		newUID := uuid.NewString() + "@aerion-caldav"
		newBlob, err := serializeVEVENT(newUID, payload.In)
		if err != nil {
			return composedVCalendar{}, fmt.Errorf("serialize new series: %w", err)
		}
		out.NewSeries = &composedNewSeries{UID: newUID, ICSBlob: newBlob}
	}

	return out, nil
}

// --- helpers ----------------------------------------------------------------

func decodeVCalendar(blob string) (*ical.Calendar, error) {
	dec := ical.NewDecoder(strings.NewReader(blob))
	cal, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("decode VCALENDAR: %w", err)
	}
	return cal, nil
}

// classifyVEvents returns the index of the master VEVENT (no RECURRENCE-ID)
// and the indices of override VEVENTs.
func classifyVEvents(cal *ical.Calendar) (masterIdx int, overrides []int) {
	masterIdx = -1
	for i, child := range cal.Children {
		if child.Name != ical.CompEvent {
			continue
		}
		if child.Props.Get(ical.PropRecurrenceID) == nil {
			if masterIdx == -1 {
				masterIdx = i
			}
			continue
		}
		overrides = append(overrides, i)
	}
	return masterIdx, overrides
}

// reindexMaster returns the (possibly shifted) index of the master VEVENT
// after children were spliced. Since the master is always the first
// VEVENT with no RECURRENCE-ID, we just re-classify.
func reindexMaster(cal *ical.Calendar, _ int) int {
	idx, _ := classifyVEvents(cal)
	return idx
}

// buildOverrideVEVENT constructs a VEVENT with RECURRENCE-ID + the
// modified fields from `in`. UID matches the master so rrule_expand /
// CalDAV clients can pair them.
func buildOverrideVEVENT(masterUID string, instanceTimeUnix int64, in EventInput) *ical.Event {
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, masterUID)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

	if in.Summary != "" {
		ev.Props.SetText(ical.PropSummary, in.Summary)
	}
	if in.Description != "" {
		ev.Props.SetText(ical.PropDescription, in.Description)
	}
	if in.Location != "" {
		ev.Props.SetText(ical.PropLocation, in.Location)
	}

	setEventStartEnd(ev, in)

	// RECURRENCE-ID matches the ORIGINAL instance time (instanceTimeUnix),
	// not the new DTSTART. Format as DATE for all-day or UTC DATE-TIME.
	setRecurrenceID(ev, instanceTimeUnix, in.IsAllDay)

	if in.Reminder != nil {
		alarm := &ical.Component{Name: ical.CompAlarm, Props: ical.Props{}}
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		trigger := ical.NewProp(ical.PropTrigger)
		trigger.Value = fmt.Sprintf("-PT%dM", in.Reminder.OffsetMinutes)
		alarm.Props.Add(trigger)
		alarm.Props.SetText(ical.PropDescription, in.Summary)
		ev.Component.Children = append(ev.Component.Children, alarm)
	}

	return ev
}

// setRecurrenceID stamps a RECURRENCE-ID property on the override VEVENT
// at instanceTimeUnix.
func setRecurrenceID(ev *ical.Event, instanceTimeUnix int64, isAllDay bool) {
	t := time.Unix(instanceTimeUnix, 0).UTC()
	prop := ical.NewProp(ical.PropRecurrenceID)
	if isAllDay {
		prop.Params.Set(ical.ParamValue, string(ical.ValueDate))
		prop.Value = t.Format("20060102")
		ev.Props.Set(prop)
		return
	}
	prop.Value = t.Format("20060102T150405Z")
	ev.Props.Set(prop)
}

// recurrenceIDMatches checks whether the override's RECURRENCE-ID
// property refers to instanceTimeUnix (UTC second precision).
func recurrenceIDMatches(prop *ical.Prop, instanceTimeUnix int64) bool {
	if prop.Params.Get(ical.ParamValue) == string(ical.ValueDate) {
		parsed, err := time.Parse("20060102", prop.Value)
		if err != nil {
			return false
		}
		return parsed.Unix() == instanceTimeUnix
	}
	// DATE-TIME — UTC or with TZID.
	tzName := prop.Params.Get(ical.ParamTimezoneID)
	loc := time.UTC
	if tzName != "" {
		if l, lerr := time.LoadLocation(tzName); lerr == nil {
			loc = l
		}
	}
	t, err := parseICSDateTime(prop.Value, loc)
	if err != nil {
		return false
	}
	return t.Unix() == instanceTimeUnix
}

// dropMatchingOverrides removes from children every override VEVENT
// whose RECURRENCE-ID matches instanceTimeUnix. Returns the new slice.
func dropMatchingOverrides(children []*ical.Component, overrideIdxs []int, instanceTimeUnix int64) []*ical.Component {
	skip := make(map[int]struct{}, len(overrideIdxs))
	for _, idx := range overrideIdxs {
		ridProp := children[idx].Props.Get(ical.PropRecurrenceID)
		if ridProp == nil {
			continue
		}
		if recurrenceIDMatches(ridProp, instanceTimeUnix) {
			skip[idx] = struct{}{}
		}
	}
	if len(skip) == 0 {
		return children
	}
	out := make([]*ical.Component, 0, len(children)-len(skip))
	for i, c := range children {
		if _, drop := skip[i]; drop {
			continue
		}
		out = append(out, c)
	}
	return out
}

// dropFutureOverrides removes overrides whose RECURRENCE-ID is at or
// after splitUnix. Used by composeThisAndFuture.
func dropFutureOverrides(children []*ical.Component, overrideIdxs []int, splitUnix int64) []*ical.Component {
	skip := make(map[int]struct{}, len(overrideIdxs))
	for _, idx := range overrideIdxs {
		ridProp := children[idx].Props.Get(ical.PropRecurrenceID)
		if ridProp == nil {
			continue
		}
		ridUnix, ok := parseRecurrenceIDUnix(ridProp)
		if !ok {
			continue
		}
		if ridUnix >= splitUnix {
			skip[idx] = struct{}{}
		}
	}
	if len(skip) == 0 {
		return children
	}
	out := make([]*ical.Component, 0, len(children)-len(skip))
	for i, c := range children {
		if _, drop := skip[i]; drop {
			continue
		}
		out = append(out, c)
	}
	return out
}

func parseRecurrenceIDUnix(prop *ical.Prop) (int64, bool) {
	if prop.Params.Get(ical.ParamValue) == string(ical.ValueDate) {
		t, err := time.Parse("20060102", prop.Value)
		if err != nil {
			return 0, false
		}
		return t.Unix(), true
	}
	tzName := prop.Params.Get(ical.ParamTimezoneID)
	loc := time.UTC
	if tzName != "" {
		if l, lerr := time.LoadLocation(tzName); lerr == nil {
			loc = l
		}
	}
	t, err := parseICSDateTime(prop.Value, loc)
	if err != nil {
		return 0, false
	}
	return t.Unix(), true
}

// addExdateProp adds an EXDATE property to the master VEVENT's props at
// the given unix time. Coalesces with any existing EXDATE values.
func addExdateProp(props ical.Props, instanceTimeUnix int64) {
	t := time.Unix(instanceTimeUnix, 0).UTC()
	exdate := t.Format("20060102T150405Z")
	existing := props.Get(ical.PropExceptionDates)
	if existing != nil {
		existing.Value = existing.Value + "," + exdate
		return
	}
	prop := ical.NewProp(ical.PropExceptionDates)
	prop.Value = exdate
	props.Add(prop)
}

// encodeICS is provided by sync.go (package-private). composeVCalendar
// uses it for re-encoding modified calendar trees; the serializeVEVENT
// helper from event_crud.go covers the new-series + single-VEVENT case.

// PushInstance for CalDAV — composes the new VCALENDAR resource(s) and
// PUTs them. For scope=this: one PUT to master.Href. For
// scope=this-and-future + update: two PUTs (clamped master + new series).
//
// Master PUT uses If-Match: master.ETag for optimistic concurrency.
// New-series PUT uses If-None-Match: * (create-only). 412 → ErrConflict;
// connection failures → ErrTransport.
func (p caldavProvider) PushInstance(ctx context.Context, src Source, cal Calendar, payload PushInstancePayload) (PushInstanceResult, error) {
	composed, err := composeVCalendar(payload)
	if err != nil {
		return PushInstanceResult{}, err
	}

	password, err := p.secrets.Get(src.ID)
	if err != nil {
		return PushInstanceResult{}, fmt.Errorf("load password: %w", err)
	}
	if password == "" {
		return PushInstanceResult{}, fmt.Errorf("no password stored for source — re-add it in settings")
	}
	// xmlfix-wrapped client — PUT responses may carry unquoted ETags on
	// some servers; without the fix the new ETag fails to parse and the
	// next conditional update breaks. Same builder as sync.
	httpClient := webdav.HTTPClientWithBasicAuth(
		davutil.NewHTTPClient(30*time.Second),
		src.Username, password,
	)

	// PUT the new master blob to master.Href.
	masterHref, err := absoluteHref(src.URL, payload.Master.Href)
	if err != nil {
		return PushInstanceResult{}, fmt.Errorf("resolve master href: %w", err)
	}
	masterETag, err := caldavPut(ctx, httpClient, masterHref, composed.MasterBlob, payload.Master.ETag, false)
	if err != nil {
		return PushInstanceResult{}, err
	}

	result := PushInstanceResult{MasterNewETag: masterETag}

	if composed.NewSeries != nil {
		// PUT the new series to a fresh resource URL.
		newHref := joinHref(cal.URL, composed.NewSeries.UID+".ics")
		newHrefAbs, err := absoluteHref(src.URL, newHref)
		if err != nil {
			return PushInstanceResult{}, fmt.Errorf("resolve new series href: %w", err)
		}
		newETag, err := caldavPut(ctx, httpClient, newHrefAbs, composed.NewSeries.ICSBlob, "", true)
		if err != nil {
			return PushInstanceResult{}, err
		}
		result.NewSeries = &NewSeriesIdentifiers{
			UID:  composed.NewSeries.UID,
			ETag: newETag,
			Href: newHref, // store the relative form locally — same as sync does
		}
	}

	return result, nil
}

// caldavPut issues a PUT with optional If-Match (for updates) or
// If-None-Match: * (for create-only). Returns the new ETag — from the
// response header when the server includes one (RFC 4791 SHOULD), or
// from a follow-up HEAD when it doesn't (common on Nextcloud). Empty
// ETag would force every subsequent conditional write through the
// retry-unconditional fallback, so we trade one HEAD round trip per
// non-compliant server for correct conflict detection on the next write.
func caldavPut(ctx context.Context, client webdav.HTTPClient, url, blob, ifMatch string, createOnly bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(blob))
	if err != nil {
		return "", fmt.Errorf("build PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	if createOnly {
		req.Header.Set("If-None-Match", "*")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("caldav PUT: %w: %v", ErrTransport, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		etag := resp.Header.Get("ETag")
		if etag == "" {
			etag = fetchETagViaHEAD(ctx, client, url)
		}
		return etag, nil
	case http.StatusPreconditionFailed:
		return "", ErrConflict
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return "", fmt.Errorf("caldav PUT %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
}
