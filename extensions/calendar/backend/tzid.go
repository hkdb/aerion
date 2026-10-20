package backend

import (
	"time"

	"github.com/emersion/go-ical"
)

// normalizeTZIDs rewrites, in place, every TZID parameter in comp and its
// children that time.LoadLocation cannot resolve. go-ical's Prop.DateTime
// (and so DateTimeStart/End and RecurrenceSet) loads the TZID itself and
// returns an error on an unknown name, ignoring the fallback location the
// caller passes — so one Outlook-style "W. Europe Standard Time" made the
// sync skip the whole event. A Windows zone ID becomes its CLDR IANA zone;
// any other unknown TZID is removed, so the value reads as floating in the
// configured display tz (the same fallback resolveLocation uses).
//
// Apply it to the decoded tree only; stored ICS blobs keep the server's
// original TZIDs.
func normalizeTZIDs(comp *ical.Component) {
	for _, props := range comp.Props {
		for i := range props {
			tzid := props[i].Params.Get(ical.ParamTimezoneID)
			if tzid == "" {
				continue
			}
			if name, ok := resolveTZID(tzid); !ok {
				props[i].Params.Del(ical.ParamTimezoneID)
			} else if name != tzid {
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
