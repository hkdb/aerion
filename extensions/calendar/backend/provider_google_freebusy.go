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

// QueryFreeBusy on a Google source. POSTs to /freeBusy with the user's
// own OAuth token; the items[].id field accepts any email address —
// Google returns busy intervals for any calendar the requesting user
// has read access to (often via the org's directory). External emails
// outside the user's domain return empty `busy` arrays without error.
func (p googleProvider) QueryFreeBusy(ctx context.Context, src Source, emails []string, fromUnix, toUnix int64) ([]FreeBusyBlock, error) {
	if len(emails) == 0 {
		return nil, nil
	}
	client, err := p.httpClient(src)
	if err != nil {
		return nil, err
	}

	items := make([]map[string]string, 0, len(emails))
	for _, e := range emails {
		t := strings.ToLower(strings.TrimSpace(e))
		if t == "" {
			continue
		}
		items = append(items, map[string]string{"id": t})
	}
	body, _ := json.Marshal(map[string]any{
		"timeMin": time.Unix(fromUnix, 0).UTC().Format(time.RFC3339),
		"timeMax": time.Unix(toUnix, 0).UTC().Format(time.RFC3339),
		"items":   items,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleAPIBase+"/freeBusy", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build freebusy request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google freebusy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("google freebusy %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Calendars map[string]struct {
			Busy []struct {
				Start string `json:"start"`
				End   string `json:"end"`
			} `json:"busy"`
		} `json:"calendars"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode google freebusy: %w", err)
	}

	var blocks []FreeBusyBlock
	for email, cal := range out.Calendars {
		for _, b := range cal.Busy {
			start, err := time.Parse(time.RFC3339, b.Start)
			if err != nil {
				continue
			}
			end, err := time.Parse(time.RFC3339, b.End)
			if err != nil {
				continue
			}
			blocks = append(blocks, FreeBusyBlock{
				Email:     strings.ToLower(email),
				StartUnix: start.Unix(),
				EndUnix:   end.Unix(),
				Status:    "BUSY",
			})
		}
	}
	return blocks, nil
}
