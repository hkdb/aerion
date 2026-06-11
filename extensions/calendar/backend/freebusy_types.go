package backend

import "context"

// FreeBusyBlock is one busy interval reported for one attendee. The
// aggregator across providers normalizes everything to this shape; the
// "Find a time" UI consumes the resulting per-attendee block lists.
type FreeBusyBlock struct {
	Email     string `json:"email"`
	StartUnix int64  `json:"startUnix"`
	EndUnix   int64  `json:"endUnix"`
	// Status: BUSY | TENTATIVE | FREE | OOF. Google freeBusy.query returns
	// busy only; Microsoft getSchedule returns availabilityView codes + per
	// item statuses; CalDAV REPORT returns VFREEBUSY entries with FBTYPE
	// param. Normalized to uppercase.
	Status string `json:"status"`
}

// FreeBusyResult bundles per-email blocks + provenance for the UI. When
// `Source` is empty AND `Blocks` is nil the aggregator couldn't get data
// for that email — the UI surfaces this as a "no data" indicator rather
// than misleading "free across the whole range".
type FreeBusyResult struct {
	Email  string          `json:"email"`
	Blocks []FreeBusyBlock `json:"blocks"`
	// Source: "google" | "microsoft" | "caldav" | "local" | "" when no
	// provider could answer.
	Source string `json:"source"`
}

// FreeBusyProvider is implemented by providers that support availability
// queries. NOT on the base Provider interface — providers without a
// free/busy surface (e.g., the local provider has its own DB-scan path)
// implement this separately. The aggregator type-checks.
type FreeBusyProvider interface {
	QueryFreeBusy(ctx context.Context, src Source, emails []string, fromUnix, toUnix int64) ([]FreeBusyBlock, error)
}
