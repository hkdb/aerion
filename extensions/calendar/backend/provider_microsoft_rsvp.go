package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// pushMicrosoftRSVP calls Graph's dedicated RSVP endpoints for the user's
// own attendance on an event others organized:
//   POST /me/events/{id}/accept             { comment, sendResponse }
//   POST /me/events/{id}/decline
//   POST /me/events/{id}/tentativelyAccept
//
// These are preferred over a generic PATCH because:
//   - they're idempotent on Graph's side (PATCHing attendees[] from the
//     attendee perspective is not well-defined);
//   - they carry `sendResponse` semantics that Graph honors as the
//     "should I notify the organizer?" flag (we default true);
//   - 202 No Content is the expected success — there's no body to parse.
//
// On 200/202/204 we return a zero-value rsvpPushResult (the RSVP endpoint
// doesn't surface a new etag). The caller's next read syncs etag drift.
func (a *API) pushMicrosoftRSVP(ctx context.Context, src Source, ev Event, partStat string) (rsvpPushResult, error) {
	if ev.ProviderEventID == "" {
		return rsvpPushResult{}, fmt.Errorf("microsoft RSVP: event has no providerEventId; cannot target")
	}
	endpoint, err := microsoftRSVPEndpoint(partStat)
	if err != nil {
		return rsvpPushResult{}, err
	}

	provider := ProviderForSource(src, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
	mp, ok := provider.(microsoftProvider)
	if !ok {
		return rsvpPushResult{}, fmt.Errorf("microsoft RSVP: provider not microsoft (got %T)", provider)
	}
	client, err := mp.httpClient(src)
	if err != nil {
		return rsvpPushResult{}, err
	}

	body, _ := json.Marshal(map[string]any{
		"comment":      "",
		"sendResponse": true,
	})
	target := microsoftGraphBase + "/me/events/" + url.PathEscape(ev.ProviderEventID) + "/" + endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return rsvpPushResult{}, fmt.Errorf("build rsvp request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := doGraphRequest(ctx, client, req)
	if err != nil {
		return rsvpPushResult{}, fmt.Errorf("graph rsvp: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return rsvpPushResult{}, nil
	case http.StatusPreconditionFailed, http.StatusConflict:
		return rsvpPushResult{}, ErrConflict
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return rsvpPushResult{}, fmt.Errorf("graph rsvp %d %s: %s",
		resp.StatusCode, resp.Status, strings.TrimSpace(string(raw)))
}

// microsoftRSVPEndpoint maps the canonical ICS PartStat to Graph's
// dedicated endpoint suffix. NEEDS-ACTION is rejected — Graph has no
// "un-RSVP" endpoint, and the UI in EventDetail only surfaces the three
// actionable choices.
func microsoftRSVPEndpoint(partStat string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(partStat)) {
	case PartStatAccepted:
		return "accept", nil
	case PartStatDeclined:
		return "decline", nil
	case PartStatTentative:
		return "tentativelyAccept", nil
	}
	return "", fmt.Errorf("microsoft RSVP: partStat %q has no endpoint", partStat)
}
