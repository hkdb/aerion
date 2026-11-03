package backend

import (
	"time"

	"github.com/emersion/go-ical"
)

// normalizeTZIDs rewrites, in place, every TZID parameter in comp and its
// children that time.LoadLocation cannot resolve.
//
// go-ical's Prop.DateTime (so DateTimeStart/End and RecurrenceSet) loads the
// TZID itself and fails on an unknown name, ignoring the fallback location
// our callers pass. Outlook writes Windows zone IDs ("W. Europe Standard
// Time"), so one such event failed to parse and the sync dropped it. A
// Windows ID becomes its CLDR IANA zone; any other unresolvable TZID is
// removed so the value reads as floating in the configured display tz, the
// same fallback resolveLocation applies.
//
// Only the decoded tree is touched; stored ICS blobs keep the server's TZIDs.
// Findings from PR #467.
func normalizeTZIDs(comp *ical.Component) {
	for _, props := range comp.Props {
		for i := range props {
			tzid := props[i].Params.Get(ical.ParamTimezoneID)
			if tzid == "" {
				continue
			}
			name, ok := resolveTZID(tzid)
			if !ok {
				props[i].Params.Del(ical.ParamTimezoneID)
				continue
			}
			if name != tzid {
				props[i].Params.Set(ical.ParamTimezoneID, name)
			}
		}
	}
	for _, child := range comp.Children {
		normalizeTZIDs(child)
	}
}

// resolveTZID returns an IANA zone name time.LoadLocation accepts for tzid,
// or false when there is none.
func resolveTZID(tzid string) (string, bool) {
	if _, err := time.LoadLocation(tzid); err == nil {
		return tzid, true
	}
	if name, ok := windowsZoneToIANA[tzid]; ok {
		return name, true
	}
	return "", false
}
