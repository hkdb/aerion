package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// QueryFreeBusy on a Microsoft Graph source. POSTs to
// /me/calendar/getSchedule with the attendee email list. Graph returns
// a per-attendee scheduleItems[] array; we normalize each to
// FreeBusyBlock. External-domain attendees return empty arrays when the
// requesting tenant doesn't have a directory relationship.
//
// availabilityViewInterval is fixed at 30 (minutes) — the per-provider
// resolution the v0.3.0 plan settled on; the scheduleItems[] are
// independent of this so the 30-min grid is just a hint to Graph.
func (p microsoftProvider) QueryFreeBusy(ctx context.Context, src Source, emails []string, fromUnix, toUnix int64) ([]FreeBusyBlock, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	client, err := p.httpClient(src)
	if err != nil {
		return nil, err
	}

	schedules := make([]string, 0, len(emails))
	for _, e := range emails {
		t := strings.ToLower(strings.TrimSpace(e))
		if t == "" {
			continue
		}
		schedules = append(schedules, t)
	}
	body, _ := json.Marshal(map[string]any{
		"schedules": schedules,
		"startTime": map[string]string{
			"dateTime": time.Unix(fromUnix, 0).UTC().Format("2006-01-02T15:04:05"),
			"timeZone": "UTC",
		},
		"endTime": map[string]string{
			"dateTime": time.Unix(toUnix, 0).UTC().Format("2006-01-02T15:04:05"),
			"timeZone": "UTC",
		},
		"availabilityViewInterval": 30,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, microsoftGraphBase+"/me/calendar/getSchedule", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build getSchedule request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("graph getSchedule: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("graph getSchedule %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Value []struct {
			ScheduleID    string `json:"scheduleId"`
			ScheduleItems []struct {
				Status string `json:"status"`
				Start  struct {
					DateTime string `json:"dateTime"`
					TimeZone string `json:"timeZone"`
				} `json:"start"`
				End struct {
					DateTime string `json:"dateTime"`
					TimeZone string `json:"timeZone"`
				} `json:"end"`
			} `json:"scheduleItems"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode graph getSchedule: %w", err)
	}

	var blocks []FreeBusyBlock
	for _, sched := range out.Value {
		email := strings.ToLower(sched.ScheduleID)
		for _, item := range sched.ScheduleItems {
			start, ok := parseGraphScheduleTime(item.Start.DateTime, item.Start.TimeZone)
			if !ok {
				continue
			}
			end, ok := parseGraphScheduleTime(item.End.DateTime, item.End.TimeZone)
			if !ok {
				continue
			}
			blocks = append(blocks, FreeBusyBlock{
				Email:     email,
				StartUnix: start.Unix(),
				EndUnix:   end.Unix(),
				Status:    graphScheduleStatusToBlock(item.Status),
			})
		}
	}
	return blocks, nil
}

// parseGraphScheduleTime: Graph's dateTime field is "YYYY-MM-DDTHH:MM:SS"
// without zone offset; the sibling timeZone field carries the IANA name.
// Falls back to UTC when either is malformed.
func parseGraphScheduleTime(dt, tz string) (time.Time, bool) {
	if dt == "" {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", dt, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// graphScheduleStatusToBlock normalizes Graph's status enum
// (free|tentative|busy|oof|workingElsewhere|unknown) to our subset.
func graphScheduleStatusToBlock(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "busy":
		return "BUSY"
	case "tentative":
		return "TENTATIVE"
	case "oof":
		return "OOF"
	case "free", "workingelsewhere":
		return "FREE"
	}
	return "BUSY"
}
