package backend

import (
	"context"
	"strings"
	"sync"
	"time"
)

// fbCacheEntry is one (email, day) row in the 5-min TTL in-memory cache.
type fbCacheEntry struct {
	blocks   []FreeBusyBlock
	source   string
	cachedAt time.Time
}

const fbCacheTTL = 5 * time.Minute

var (
	fbCacheMu sync.Mutex
	fbCache   = make(map[string]fbCacheEntry)
)

// fbCacheKey buckets by (email, calendar day). The 5-min TTL keeps results
// fresh while letting a "Find a time" UI page through hours without
// re-hitting the provider for every drag.
func fbCacheKey(email string, dayUnix int64) string {
	return strings.ToLower(strings.TrimSpace(email)) + "|" + time.Unix(dayUnix, 0).UTC().Format("2006-01-02")
}

// QueryAggregatedFreeBusy is the API-level surface — gathers free/busy
// blocks for each attendee email by routing to whichever provider can
// answer for that email's domain. The aggregator favors:
//  1. The user's own identity emails → local DB scan (queryLocalFreeBusy).
//  2. Emails matching the domain of any Google source's user → that
//     source's Google freeBusy.query.
//  3. Emails matching the domain of any Microsoft source's user → Graph
//     getSchedule.
//  4. Fall-through: try every Google + Microsoft source we have; the first
//     that returns non-empty wins. Empty results from every provider are
//     surfaced as a "no data" result rather than misleading "free".
//
// 5-min cache by (email, calendar-day). Cache misses fan out per provider.
func (a *API) QueryAggregatedFreeBusy(ctx context.Context, selfEmails, attendeeEmails []string, fromUnix, toUnix int64) ([]FreeBusyResult, error) {
	if len(attendeeEmails) == 0 {
		return nil, nil
	}

	selfSet := make(map[string]struct{}, len(selfEmails))
	for _, e := range selfEmails {
		t := strings.ToLower(strings.TrimSpace(e))
		if t == "" {
			continue
		}
		selfSet[t] = struct{}{}
	}

	sources, err := a.store.ListSources()
	if err != nil {
		return nil, err
	}

	// Pre-resolve provider lists once.
	var googleSources, microsoftSources []Source
	for _, src := range sources {
		switch src.Type {
		case SourceTypeGoogle:
			googleSources = append(googleSources, src)
		case SourceTypeMicrosoft:
			microsoftSources = append(microsoftSources, src)
		}
	}

	out := make([]FreeBusyResult, 0, len(attendeeEmails))
	for _, raw := range attendeeEmails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" {
			continue
		}

		// Cache check.
		key := fbCacheKey(email, fromUnix)
		fbCacheMu.Lock()
		cached, ok := fbCache[key]
		fbCacheMu.Unlock()
		if ok && time.Since(cached.cachedAt) < fbCacheTTL {
			out = append(out, FreeBusyResult{Email: email, Blocks: cached.blocks, Source: cached.source})
			continue
		}

		// Route. Self-emails get the local scan; others fan out across
		// every provider source. The first non-empty answer wins —
		// providers' empty-results-for-foreign-domains are honored.
		var blocks []FreeBusyBlock
		var src string
		if _, isSelf := selfSet[email]; isSelf {
			localBlocks, _ := a.queryLocalFreeBusy(ctx, []string{email}, fromUnix, toUnix)
			if len(localBlocks) > 0 {
				blocks, src = localBlocks, "local"
			}
		}
		if len(blocks) == 0 {
			for _, gs := range googleSources {
				provider := ProviderForSource(gs, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
				fp, ok := provider.(FreeBusyProvider)
				if !ok {
					continue
				}
				bs, err := fp.QueryFreeBusy(ctx, gs, []string{email}, fromUnix, toUnix)
				if err != nil || len(bs) == 0 {
					continue
				}
				blocks, src = bs, "google"
				break
			}
		}
		if len(blocks) == 0 {
			for _, ms := range microsoftSources {
				provider := ProviderForSource(ms, ProviderDeps{Store: a.store, Secrets: a.secrets, Auth: a.auth})
				fp, ok := provider.(FreeBusyProvider)
				if !ok {
					continue
				}
				bs, err := fp.QueryFreeBusy(ctx, ms, []string{email}, fromUnix, toUnix)
				if err != nil || len(bs) == 0 {
					continue
				}
				blocks, src = bs, "microsoft"
				break
			}
		}

		// Cache + emit. Empty blocks + empty source means "no data" — UI
		// renders a tag rather than implicit free-across-the-board.
		fbCacheMu.Lock()
		fbCache[key] = fbCacheEntry{blocks: blocks, source: src, cachedAt: time.Now()}
		fbCacheMu.Unlock()

		out = append(out, FreeBusyResult{Email: email, Blocks: blocks, Source: src})
	}

	return out, nil
}
