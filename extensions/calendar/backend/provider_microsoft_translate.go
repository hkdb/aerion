package backend

// ICS ↔ Microsoft Graph event JSON translation.
//
// Mirrors the Google translation file's shape but maps to Graph's
// structured `recurrence` object (pattern + range) instead of an RRULE
// array. The composer emits FREQ=DAILY/WEEKLY/MONTHLY/YEARLY with optional
// UNTIL or COUNT and no custom BYDAY/BYMONTHDAY today, so the write path
// (RRULE → Graph object) maps cleanly. Server-originated recurrence
// objects round-trip best-effort with graceful fallback for the rarer
// relative-monthly / relative-yearly patterns.
//
// Time handling: outgoing requests always set
// `Prefer: outlook.timezone="UTC"` so incoming dateTime values arrive in
// UTC and outgoing values are sent in UTC. This sidesteps Windows
// timezone name mapping (Microsoft accepts both IANA and Windows names
// depending on the header). The display-layer tz handling
// (calendarSettings.effectiveTimezone) handles user-visible formatting.

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// graphEvent is the JSON shape for /me/events resource. Only fields
// Aerion reads/writes are modeled.
type graphEvent struct {
	ID                          string             `json:"id,omitempty"`
	ICalUID                     string             `json:"iCalUId,omitempty"` // Graph's mixed-case key
	ETag                        string             `json:"@odata.etag,omitempty"`
	Subject                     string             `json:"subject,omitempty"`
	Body                        *graphBody         `json:"body,omitempty"`
	Location                    *graphLocation     `json:"location,omitempty"`
	Start                       *graphTimePoint    `json:"start,omitempty"`
	End                         *graphTimePoint    `json:"end,omitempty"`
	IsAllDay                    *bool              `json:"isAllDay,omitempty"`
	Recurrence                  *graphRecurrence   `json:"recurrence"`
	ReminderMinutesBeforeStart  *int               `json:"reminderMinutesBeforeStart,omitempty"`
	IsReminderOn                *bool              `json:"isReminderOn,omitempty"`
	SeriesMasterID              string             `json:"seriesMasterId,omitempty"`
	Type                        string             `json:"type,omitempty"` // "singleInstance" | "seriesMaster" | "exception" | "occurrence"
	Status                      *graphEventStatus  `json:"@removed,omitempty"`
	Attendees                   []graphAttendee    `json:"attendees,omitempty"`
	Organizer                   *graphRecipient    `json:"organizer,omitempty"`
}

// graphAttendee is Graph's per-attendee shape:
//   { emailAddress: { address, name }, type: "required"|"optional"|"resource",
//     status: { response, time } }
// status.response: none|organizer|tentativelyAccepted|accepted|declined|notResponded.
type graphAttendee struct {
	EmailAddress graphEmailAddress    `json:"emailAddress"`
	Type         string               `json:"type,omitempty"`
	Status       *graphResponseStatus `json:"status,omitempty"`
}

// graphRecipient is Graph's organizer shape — same nested emailAddress
// as graphAttendee but without type/status. Modeled separately to match
// the wire form precisely.
type graphRecipient struct {
	EmailAddress graphEmailAddress `json:"emailAddress"`
}

type graphEmailAddress struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

type graphResponseStatus struct {
	Response string `json:"response,omitempty"` // see graphPartStatToICS
	Time     string `json:"time,omitempty"`
}

// graphEventStatus is set on delta-removed entries (Graph's delta
// endpoint encodes deletes via an `@removed` object rather than a
// status string).
type graphEventStatus struct {
	Reason string `json:"reason,omitempty"`
}

type graphBody struct {
	ContentType string `json:"contentType"` // "html" | "text"
	Content     string `json:"content"`
}

type graphLocation struct {
	DisplayName string `json:"displayName,omitempty"`
}

type graphTimePoint struct {
	DateTime string `json:"dateTime"`           // ISO-8601 without offset; pair with TimeZone
	TimeZone string `json:"timeZone,omitempty"` // IANA when Prefer header set; "UTC" by default
}

type graphRecurrence struct {
	Pattern graphPattern `json:"pattern"`
	Range   graphRange   `json:"range"`
}

type graphPattern struct {
	Type           string   `json:"type"` // daily | weekly | absoluteMonthly | relativeMonthly | absoluteYearly | relativeYearly
	Interval       int      `json:"interval"`
	DaysOfWeek     []string `json:"daysOfWeek,omitempty"`
	DayOfMonth     int      `json:"dayOfMonth,omitempty"`
	Month          int      `json:"month,omitempty"`
	FirstDayOfWeek string   `json:"firstDayOfWeek,omitempty"`
	Index          string   `json:"index,omitempty"`
}

type graphRange struct {
	Type                string `json:"type"` // endDate | noEnd | numbered
	StartDate           string `json:"startDate,omitempty"`           // YYYY-MM-DD
	EndDate             string `json:"endDate,omitempty"`             // YYYY-MM-DD
	NumberOfOccurrences int    `json:"numberOfOccurrences,omitempty"`
	RecurrenceTimeZone  string `json:"recurrenceTimeZone,omitempty"`
}

// microsoftCalendarListEntry is one item returned from /me/calendars.
type microsoftCalendarListEntry struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	CanEdit           bool   `json:"canEdit"`
	IsDefaultCalendar bool   `json:"isDefaultCalendar"`
}

// errMicrosoftEventCancelled is returned by translateGraphEventToICS for
// delta-removed entries. The sync caller treats it as a delete.
var errMicrosoftEventCancelled = errors.New("microsoft event cancelled / removed")

// --- ICS → Graph (write direction) -----------------------------------------

// translateICSToGraphEvent extracts the master VEVENT from a single-VEVENT
// VCALENDAR blob (built by event_crud.go's serializeVEVENT) and produces
// the Graph JSON shape suitable for POST/PATCH /me/events.
func translateICSToGraphEvent(icsBlob string) (graphEvent, error) {
	dec := ical.NewDecoder(strings.NewReader(icsBlob))
	cal, err := dec.Decode()
	if err != nil {
		return graphEvent{}, fmt.Errorf("ical decode: %w", err)
	}
	events := cal.Events()
	if len(events) == 0 {
		return graphEvent{}, fmt.Errorf("no VEVENT in ICS blob")
	}
	ev := events[0]

	out := graphEvent{
		ICalUID: propText(&ev, ical.PropUID),
		Subject: propText(&ev, ical.PropSummary),
	}

	if descr := propText(&ev, ical.PropDescription); descr != "" {
		out.Body = &graphBody{ContentType: "text", Content: descr}
	}
	if loc := propText(&ev, ical.PropLocation); loc != "" {
		out.Location = &graphLocation{DisplayName: loc}
	}

	start, end, isAllDay, err := extractGraphTimes(&ev)
	if err != nil {
		return graphEvent{}, err
	}
	out.Start = start
	out.End = end
	allDayPtr := isAllDay
	out.IsAllDay = &allDayPtr

	if rrule := propText(&ev, ical.PropRecurrenceRule); rrule != "" {
		dtstart, _ := startTimeForRRule(&ev)
		rec, rerr := rruleToGraphRecurrence(rrule, dtstart)
		if rerr != nil {
			return graphEvent{}, fmt.Errorf("translate RRULE: %w", rerr)
		}
		out.Recurrence = rec
	}

	// First DISPLAY VALARM → reminderMinutesBeforeStart (Graph allows ONE).
	for _, child := range ev.Component.Children {
		if child.Name != ical.CompAlarm {
			continue
		}
		if propTextOnComp(child, ical.PropAction) != "DISPLAY" {
			continue
		}
		minutes, ok := parseTriggerMinutes(propTextOnComp(child, ical.PropTrigger))
		if !ok {
			continue
		}
		m := minutes
		reminderOn := true
		out.ReminderMinutesBeforeStart = &m
		out.IsReminderOn = &reminderOn
		break
	}

	// Attendees + Organizer via the shared ICS parser. Role
	// REQ-PARTICIPANT → required (Graph default); OPT-PARTICIPANT →
	// optional; CUTYPE=RESOURCE|ROOM → resource. PartStat via the
	// centralized map.
	if atts := parseAttendeesFromVEVENT(&ev); len(atts) > 0 {
		out.Attendees = make([]graphAttendee, 0, len(atts))
		for _, a := range atts {
			ga := graphAttendee{
				EmailAddress: graphEmailAddress{Address: a.Email, Name: a.CommonName},
				Type:         "required",
				Status:       &graphResponseStatus{Response: icsPartStatToGraph(a.PartStat)},
			}
			if strings.EqualFold(a.Role, RoleOptParticipant) {
				ga.Type = "optional"
			}
			if strings.EqualFold(a.CUType, CUTypeResource) || strings.EqualFold(a.CUType, CUTypeRoom) {
				ga.Type = "resource"
			}
			out.Attendees = append(out.Attendees, ga)
		}
	}
	if org := parseOrganizerFromVEVENT(&ev); org != nil {
		out.Organizer = &graphRecipient{
			EmailAddress: graphEmailAddress{Address: org.Email, Name: org.CommonName},
		}
	}

	return out, nil
}

// extractGraphTimes converts ICS DTSTART/DTEND to Graph time points.
// All times go out in UTC with TimeZone="UTC" to match the Prefer header
// we set on every request.
func extractGraphTimes(ev *ical.Event) (*graphTimePoint, *graphTimePoint, bool, error) {
	startProp := ev.Props.Get(ical.PropDateTimeStart)
	endProp := ev.Props.Get(ical.PropDateTimeEnd)
	if startProp == nil || endProp == nil {
		return nil, nil, false, fmt.Errorf("VEVENT missing DTSTART/DTEND")
	}
	isAllDay := startProp.Params.Get(ical.ParamValue) == string(ical.ValueDate)

	start, err := icsPropToGraphTime(startProp, isAllDay)
	if err != nil {
		return nil, nil, false, fmt.Errorf("convert DTSTART: %w", err)
	}
	end, err := icsPropToGraphTime(endProp, isAllDay)
	if err != nil {
		return nil, nil, false, fmt.Errorf("convert DTEND: %w", err)
	}
	return start, end, isAllDay, nil
}

func icsPropToGraphTime(p *ical.Prop, isAllDay bool) (*graphTimePoint, error) {
	if isAllDay {
		// Graph wants ISO 8601 dateTime even for all-day events.
		parsed, err := time.Parse("20060102", p.Value)
		if err != nil {
			return nil, err
		}
		return &graphTimePoint{
			DateTime: parsed.Format("2006-01-02T15:04:05.0000000"),
			TimeZone: "UTC",
		}, nil
	}
	tzName := p.Params.Get(ical.ParamTimezoneID)
	loc := time.UTC
	if tzName != "" {
		if l, lerr := time.LoadLocation(tzName); lerr == nil {
			loc = l
		}
	}
	t, perr := parseICSDateTime(p.Value, loc)
	if perr != nil {
		return nil, perr
	}
	return &graphTimePoint{
		DateTime: t.UTC().Format("2006-01-02T15:04:05.0000000"),
		TimeZone: "UTC",
	}, nil
}

// startTimeForRRule returns the DTSTART parsed as a time.Time in its
// original timezone, used by rruleToGraphRecurrence to derive weekday,
// dayOfMonth, month from the start.
func startTimeForRRule(ev *ical.Event) (time.Time, error) {
	p := ev.Props.Get(ical.PropDateTimeStart)
	if p == nil {
		return time.Time{}, fmt.Errorf("missing DTSTART")
	}
	if p.Params.Get(ical.ParamValue) == string(ical.ValueDate) {
		return time.Parse("20060102", p.Value)
	}
	tzName := p.Params.Get(ical.ParamTimezoneID)
	loc := time.UTC
	if tzName != "" {
		if l, lerr := time.LoadLocation(tzName); lerr == nil {
			loc = l
		}
	}
	return parseICSDateTime(p.Value, loc)
}

// rruleToGraphRecurrence converts an RRULE value (without the "RRULE:"
// prefix) plus the DTSTART time to a Graph recurrence object. The composer
// emits at most FREQ + UNTIL or COUNT today; this function also handles
// BYDAY in case server-originated RRULEs round-trip through it.
func rruleToGraphRecurrence(rrule string, dtstart time.Time) (*graphRecurrence, error) {
	parts := parseRRuleParts(rrule)
	freq := parts["FREQ"]
	if freq == "" {
		return nil, fmt.Errorf("RRULE missing FREQ")
	}

	pattern, err := buildGraphPattern(freq, parts, dtstart)
	if err != nil {
		return nil, err
	}
	rng := buildGraphRange(parts, dtstart)
	return &graphRecurrence{Pattern: pattern, Range: rng}, nil
}

func parseRRuleParts(rrule string) map[string]string {
	out := make(map[string]string)
	for _, seg := range strings.Split(rrule, ";") {
		eq := strings.IndexByte(seg, '=')
		if eq <= 0 {
			continue
		}
		out[strings.ToUpper(seg[:eq])] = seg[eq+1:]
	}
	return out
}

func buildGraphPattern(freq string, parts map[string]string, dtstart time.Time) (graphPattern, error) {
	interval := 1
	if iv, ok := parts["INTERVAL"]; ok {
		if n, err := strconv.Atoi(iv); err == nil && n > 0 {
			interval = n
		}
	}

	switch strings.ToUpper(freq) {
	case "DAILY":
		return graphPattern{Type: "daily", Interval: interval}, nil
	case "WEEKLY":
		days := parseBYDAY(parts["BYDAY"])
		if len(days) == 0 {
			days = []string{weekdayToGraph(dtstart.Weekday())}
		}
		return graphPattern{Type: "weekly", Interval: interval, DaysOfWeek: days}, nil
	case "MONTHLY":
		dom := dtstart.Day()
		if bmd, ok := parts["BYMONTHDAY"]; ok {
			if n, err := strconv.Atoi(strings.SplitN(bmd, ",", 2)[0]); err == nil && n > 0 {
				dom = n
			}
		}
		return graphPattern{Type: "absoluteMonthly", Interval: interval, DayOfMonth: dom}, nil
	case "YEARLY":
		return graphPattern{
			Type:       "absoluteYearly",
			Interval:   interval,
			DayOfMonth: dtstart.Day(),
			Month:      int(dtstart.Month()),
		}, nil
	}
	return graphPattern{}, fmt.Errorf("unsupported FREQ %q", freq)
}

func buildGraphRange(parts map[string]string, dtstart time.Time) graphRange {
	startDate := dtstart.Format("2006-01-02")
	if until, ok := parts["UNTIL"]; ok {
		// UNTIL is YYYYMMDD or YYYYMMDDTHHMMSSZ. Take the date portion.
		datePart := until
		if t := strings.IndexByte(until, 'T'); t > 0 {
			datePart = until[:t]
		}
		if d, err := time.Parse("20060102", datePart); err == nil {
			return graphRange{
				Type:      "endDate",
				StartDate: startDate,
				EndDate:   d.Format("2006-01-02"),
			}
		}
	}
	if count, ok := parts["COUNT"]; ok {
		if n, err := strconv.Atoi(count); err == nil && n > 0 {
			return graphRange{
				Type:                "numbered",
				StartDate:           startDate,
				NumberOfOccurrences: n,
			}
		}
	}
	return graphRange{Type: "noEnd", StartDate: startDate}
}

// parseBYDAY splits a BYDAY value like "MO,WE,FR" into Graph day names.
// Strips ordinal prefixes like "2MO" → "monday" (Aerion composer doesn't
// emit ordinals today; defensive for server-originated values).
func parseBYDAY(byday string) []string {
	if byday == "" {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(byday, ",") {
		code := strings.TrimLeft(strings.ToUpper(strings.TrimSpace(raw)), "+-0123456789")
		day := icsDayCodeToGraph(code)
		if day != "" {
			out = append(out, day)
		}
	}
	return out
}

func weekdayToGraph(w time.Weekday) string {
	switch w {
	case time.Sunday:
		return "sunday"
	case time.Monday:
		return "monday"
	case time.Tuesday:
		return "tuesday"
	case time.Wednesday:
		return "wednesday"
	case time.Thursday:
		return "thursday"
	case time.Friday:
		return "friday"
	case time.Saturday:
		return "saturday"
	}
	return "monday"
}

func icsDayCodeToGraph(code string) string {
	switch code {
	case "SU":
		return "sunday"
	case "MO":
		return "monday"
	case "TU":
		return "tuesday"
	case "WE":
		return "wednesday"
	case "TH":
		return "thursday"
	case "FR":
		return "friday"
	case "SA":
		return "saturday"
	}
	return ""
}

func graphDayToICSCode(day string) string {
	switch strings.ToLower(day) {
	case "sunday":
		return "SU"
	case "monday":
		return "MO"
	case "tuesday":
		return "TU"
	case "wednesday":
		return "WE"
	case "thursday":
		return "TH"
	case "friday":
		return "FR"
	case "saturday":
		return "SA"
	}
	return ""
}

// --- Graph → ICS (read direction) -------------------------------------------

// translateGraphEventToICS converts ONE Graph event JSON into a
// single-VEVENT VCALENDAR ICS blob.
//
// Delta-removed entries (the @removed field) return
// errMicrosoftEventCancelled so the sync caller treats them as deletions.
func translateGraphEventToICS(ev graphEvent) (string, error) {
	if ev.Status != nil {
		return "", errMicrosoftEventCancelled
	}
	if ev.ICalUID == "" {
		return "", fmt.Errorf("graph event missing iCalUId")
	}
	if ev.Start == nil || ev.End == nil {
		return "", fmt.Errorf("graph event missing start/end")
	}

	icalEv := ical.NewEvent()
	icalEv.Props.SetText(ical.PropUID, ev.ICalUID)
	icalEv.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())

	if ev.Subject != "" {
		icalEv.Props.SetText(ical.PropSummary, ev.Subject)
	}
	if ev.Body != nil && ev.Body.Content != "" {
		icalEv.Props.SetText(ical.PropDescription, ev.Body.Content)
	}
	if ev.Location != nil && ev.Location.DisplayName != "" {
		icalEv.Props.SetText(ical.PropLocation, ev.Location.DisplayName)
	}

	isAllDay := ev.IsAllDay != nil && *ev.IsAllDay
	if err := setICSTimeFromGraph(icalEv, ical.PropDateTimeStart, ev.Start, isAllDay); err != nil {
		return "", fmt.Errorf("translate start: %w", err)
	}
	if err := setICSTimeFromGraph(icalEv, ical.PropDateTimeEnd, ev.End, isAllDay); err != nil {
		return "", fmt.Errorf("translate end: %w", err)
	}

	if ev.Recurrence != nil {
		rrule := graphRecurrenceToRRule(ev.Recurrence)
		if rrule != "" {
			setRRuleText(icalEv.Props, rrule)
		}
	}

	if ev.ReminderMinutesBeforeStart != nil && ev.IsReminderOn != nil && *ev.IsReminderOn {
		alarm := &ical.Component{Name: ical.CompAlarm, Props: ical.Props{}}
		alarm.Props.SetText(ical.PropAction, "DISPLAY")
		trigger := ical.NewProp(ical.PropTrigger)
		trigger.Value = fmt.Sprintf("-PT%dM", *ev.ReminderMinutesBeforeStart)
		alarm.Props.Add(trigger)
		if ev.Subject != "" {
			alarm.Props.SetText(ical.PropDescription, ev.Subject)
		}
		icalEv.Component.Children = append(icalEv.Component.Children, alarm)
	}

	// Organizer + Attendees → ATTENDEE/ORGANIZER ICS lines via the shared
	// emit helper. Graph's `type` enum maps to ROLE; `status.response` to
	// PARTSTAT via the centralized map.
	var orgInput *OrganizerInput
	if ev.Organizer != nil && ev.Organizer.EmailAddress.Address != "" {
		orgInput = &OrganizerInput{
			Email:      ev.Organizer.EmailAddress.Address,
			CommonName: ev.Organizer.EmailAddress.Name,
		}
	}
	if len(ev.Attendees) > 0 || orgInput != nil {
		atts := make([]AttendeeInput, 0, len(ev.Attendees))
		for _, a := range ev.Attendees {
			ai := AttendeeInput{
				Email:      a.EmailAddress.Address,
				CommonName: a.EmailAddress.Name,
				Role:       RoleReqParticipant,
			}
			if strings.EqualFold(a.Type, "optional") {
				ai.Role = RoleOptParticipant
			}
			if strings.EqualFold(a.Type, "resource") {
				ai.CUType = CUTypeResource
			}
			if a.Status != nil {
				ai.PartStat = graphPartStatToICS(a.Status.Response)
			}
			atts = append(atts, ai)
		}
		emitAttendeesIntoVEVENT(icalEv, orgInput, atts)
	}

	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Aerion//Calendar Extension//EN")
	cal.Children = append(cal.Children, icalEv.Component)

	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func setICSTimeFromGraph(ev *ical.Event, propName string, tp *graphTimePoint, isAllDay bool) error {
	if tp == nil {
		return fmt.Errorf("time point required")
	}
	if isAllDay {
		// Graph sends "YYYY-MM-DDTHH:MM:SS.0000000" even for all-day; trim
		// to the date portion.
		datePart := tp.DateTime
		if t := strings.IndexByte(datePart, 'T'); t > 0 {
			datePart = datePart[:t]
		}
		parsed, err := time.Parse("2006-01-02", datePart)
		if err != nil {
			return fmt.Errorf("parse all-day date %q: %w", tp.DateTime, err)
		}
		prop := ical.NewProp(propName)
		prop.Params.Set(ical.ParamValue, string(ical.ValueDate))
		prop.Value = parsed.Format("20060102")
		ev.Props.Set(prop)
		return nil
	}
	parsed, err := parseGraphDateTime(tp.DateTime)
	if err != nil {
		return fmt.Errorf("parse dateTime %q: %w", tp.DateTime, err)
	}
	// With Prefer: outlook.timezone="UTC", incoming dateTimes are UTC
	// regardless of the TimeZone string. Store as UTC ICS.
	ev.Props.SetDateTime(propName, parsed.UTC())
	return nil
}

// parseGraphDateTime accepts Graph's "YYYY-MM-DDTHH:MM:SS.0000000" form
// (no offset, no Z) and a few common variants.
func parseGraphDateTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02T15:04:05.0000000",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized Graph dateTime format")
}

// graphRecurrenceToRRule converts a Graph recurrence object to an RRULE
// value (without the "RRULE:" prefix). Returns empty string on
// unrecognized patterns rather than erroring; the caller can still
// upsert the master event without recurrence and rrule_expand will treat
// it as a single occurrence.
func graphRecurrenceToRRule(rec *graphRecurrence) string {
	if rec == nil {
		return ""
	}
	parts := []string{}
	freq := graphPatternTypeToFreq(rec.Pattern.Type)
	if freq == "" {
		return ""
	}
	parts = append(parts, "FREQ="+freq)
	if rec.Pattern.Interval > 1 {
		parts = append(parts, fmt.Sprintf("INTERVAL=%d", rec.Pattern.Interval))
	}
	if (rec.Pattern.Type == "weekly" || strings.HasPrefix(rec.Pattern.Type, "relative")) && len(rec.Pattern.DaysOfWeek) > 0 {
		codes := make([]string, 0, len(rec.Pattern.DaysOfWeek))
		for _, d := range rec.Pattern.DaysOfWeek {
			if code := graphDayToICSCode(d); code != "" {
				codes = append(codes, code)
			}
		}
		if len(codes) > 0 {
			parts = append(parts, "BYDAY="+strings.Join(codes, ","))
		}
	}
	if rec.Pattern.Type == "absoluteMonthly" && rec.Pattern.DayOfMonth > 0 {
		parts = append(parts, fmt.Sprintf("BYMONTHDAY=%d", rec.Pattern.DayOfMonth))
	}

	switch rec.Range.Type {
	case "endDate":
		if d, err := time.Parse("2006-01-02", rec.Range.EndDate); err == nil {
			parts = append(parts, "UNTIL="+d.Format("20060102")+"T235959Z")
		}
	case "numbered":
		if rec.Range.NumberOfOccurrences > 0 {
			parts = append(parts, fmt.Sprintf("COUNT=%d", rec.Range.NumberOfOccurrences))
		}
	}
	return strings.Join(parts, ";")
}

func graphPatternTypeToFreq(t string) string {
	switch strings.ToLower(t) {
	case "daily":
		return "DAILY"
	case "weekly":
		return "WEEKLY"
	case "absolutemonthly", "relativemonthly":
		return "MONTHLY"
	case "absoluteyearly", "relativeyearly":
		return "YEARLY"
	}
	return ""
}
