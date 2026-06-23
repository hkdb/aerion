package carddav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"
	"github.com/hkdb/aerion/internal/kit/davutil"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// newHTTPClient builds an HTTP client with the WebDAV xmlfix transport
// applied. Both this package and calendar's CalDAV provider use the same
// shared shape — see internal/kit/davutil. The local helper stays as a
// thin alias so existing call sites don't churn.
func newHTTPClient(timeout time.Duration) *http.Client {
	return davutil.NewHTTPClient(timeout)
}

// Client wraps the CardDAV client with discovery and convenience methods
type Client struct {
	client   *carddav.Client
	baseURL  string
	username string
	password string
	log      zerolog.Logger
}

// NewClient creates a new CardDAV client
func NewClient(baseURL, username, password string) (*Client, error) {
	// Create HTTP client with XML-fix transport
	httpClient := newHTTPClient(30 * time.Second)

	// Parse and normalize the URL
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Ensure scheme is present
	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}

	client, err := carddav.NewClient(
		webdav.HTTPClientWithBasicAuth(httpClient, username, password),
		parsedURL.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CardDAV client: %w", err)
	}

	return &Client{
		client:   client,
		baseURL:  parsedURL.String(),
		username: username,
		password: password,
		log:      logging.WithComponent("carddav-client"),
	}, nil
}

// DiscoverAddressbooks discovers all addressbooks from the server
// It tries multiple discovery methods:
// 1. .well-known/carddav
// 2. Direct PROPFIND on the URL
// 3. Common paths (/remote.php/dav for Nextcloud, etc.)
func DiscoverAddressbooks(baseURL, username, password string) ([]AddressbookInfo, error) {
	ctx := context.Background()
	log := logging.WithComponent("carddav-discovery")
	log.Info().Str("url", baseURL).Msg("Starting addressbook discovery")

	// Parse URL
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}

	// Create HTTP client with XML-fix transport
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(30*time.Second),
		username, password,
	)

	// Try discovery methods in order
	var addressbooks []AddressbookInfo

	// Method 1: Try the URL as-is (might be a direct addressbook URL or principal)
	addressbooks, err = tryDiscoverFromURL(ctx, httpClient, parsedURL.String(), log)
	if err == nil && len(addressbooks) > 0 {
		return addressbooks, nil
	}
	log.Debug().Err(err).Msg("Direct URL discovery failed, trying .well-known")

	// Method 2: Try .well-known/carddav
	wellKnownURL := fmt.Sprintf("%s://%s/.well-known/carddav", parsedURL.Scheme, parsedURL.Host)
	addressbooks, err = tryDiscoverFromURL(ctx, httpClient, wellKnownURL, log)
	if err == nil && len(addressbooks) > 0 {
		return addressbooks, nil
	}
	log.Debug().Err(err).Msg(".well-known discovery failed, trying common paths")

	// Method 3: Try common CardDAV paths
	commonPaths := []string{
		"/remote.php/dav",     // Nextcloud/ownCloud
		"/remote.php/carddav", // Older Nextcloud
		fmt.Sprintf("/remote.php/dav/addressbooks/users/%s/", username), // Nextcloud direct
		"/dav",                    // Generic
		"/carddav",                // Generic
		"/principals/" + username, // Some servers
	}

	for _, path := range commonPaths {
		tryURL := fmt.Sprintf("%s://%s%s", parsedURL.Scheme, parsedURL.Host, path)
		addressbooks, err = tryDiscoverFromURL(ctx, httpClient, tryURL, log)
		if err == nil && len(addressbooks) > 0 {
			return addressbooks, nil
		}
	}

	return nil, fmt.Errorf("no addressbooks found at %s", baseURL)
}

// tryDiscoverFromURL attempts to discover addressbooks from a specific URL
func tryDiscoverFromURL(ctx context.Context, httpClient webdav.HTTPClient, urlStr string, log zerolog.Logger) ([]AddressbookInfo, error) {
	log.Debug().Str("url", urlStr).Msg("Trying discovery from URL")

	client, err := carddav.NewClient(httpClient, urlStr)
	if err != nil {
		return nil, err
	}

	// Try to find the current user's principal
	principal, err := client.FindCurrentUserPrincipal(ctx)
	if err != nil {
		log.Debug().Err(err).Msg("FindCurrentUserPrincipal failed")
		// Try the URL directly as addressbook home
		return tryListAddressbooksAt(ctx, httpClient, urlStr, log)
	}

	log.Debug().Str("principal", principal).Msg("Found principal")

	// Find addressbook home set
	homeSet, err := client.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		log.Debug().Err(err).Msg("FindAddressBookHomeSet failed")
		return nil, err
	}

	log.Debug().Str("homeSet", homeSet).Msg("Found addressbook home set")

	// List addressbooks in the home set
	return tryListAddressbooksAt(ctx, httpClient, resolveURL(urlStr, homeSet), log)
}

// tryListAddressbooksAt lists addressbooks at a specific URL
func tryListAddressbooksAt(ctx context.Context, httpClient webdav.HTTPClient, urlStr string, log zerolog.Logger) ([]AddressbookInfo, error) {
	client, err := carddav.NewClient(httpClient, urlStr)
	if err != nil {
		return nil, err
	}

	// Extract path from URL - FindAddressBooks expects a path, not a full URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	log.Debug().Str("url", urlStr).Str("path", parsedURL.Path).Msg("Listing addressbooks")

	addressbooks, err := client.FindAddressBooks(ctx, parsedURL.Path)
	if err != nil {
		return nil, err
	}

	var result []AddressbookInfo
	for _, ab := range addressbooks {
		info := AddressbookInfo{
			Path:        ab.Path,
			Name:        ab.Name,
			Description: ab.Description,
		}
		if info.Name == "" {
			// Use the last path segment as the name
			parts := strings.Split(strings.Trim(ab.Path, "/"), "/")
			if len(parts) > 0 {
				info.Name = parts[len(parts)-1]
			}
		}
		result = append(result, info)
		log.Debug().Str("path", ab.Path).Str("name", ab.Name).Msg("Found addressbook")
	}

	return result, nil
}

// resolveURL resolves a potentially relative URL against a base URL
func resolveURL(baseURL, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return path
	}

	ref, err := url.Parse(path)
	if err != nil {
		return path
	}

	return base.ResolveReference(ref).String()
}

// FetchContacts fetches all contacts from an addressbook. Returns one
// ParsedRecord per vCard (no per-email fan-out).
func (c *Client) FetchContacts(addressbookPath string) ([]*ParsedRecord, error) {
	ctx := context.Background()
	c.log.Debug().Str("path", addressbookPath).Msg("Fetching contacts")

	// Resolve the addressbook path against the base URL
	fullPath := resolveURL(c.baseURL, addressbookPath)

	// Create a new client for this specific addressbook
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)

	abClient, err := carddav.NewClient(httpClient, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for addressbook: %w", err)
	}

	// Query all address objects
	query := &carddav.AddressBookQuery{
		DataRequest: carddav.AddressDataRequest{
			AllProp: true,
		},
	}

	addressObjects, err := abClient.QueryAddressBook(ctx, addressbookPath, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query addressbook: %w", err)
	}

	c.log.Debug().Int("count", len(addressObjects)).Msg("Fetched address objects")

	records := make([]*ParsedRecord, 0, len(addressObjects))
	for _, obj := range addressObjects {
		parsed := parseVCard(obj)
		if parsed == nil {
			continue
		}
		records = append(records, parsed)
	}

	c.log.Info().Int("records", len(records)).Str("path", addressbookPath).Msg("Parsed records from addressbook")
	return records, nil
}

// ParsedRecord is the full vCard parse result — one ParsedRecord per vCard,
// carrying all standard fields the Phase 2b.2.a multi-field schema can hold.
// Replaces the legacy ParsedContact (one-per-email fan-out shape).
//
// VCardRaw stores the re-encoded vCard body for round-trip preservation: when
// 2b.2.b adds write paths, we parse vcard_raw to surface unknown properties
// and mutate only the known field set.
type ParsedRecord struct {
	Href     string
	ETag     string
	VCardRaw string

	FN       string
	NGiven   string
	NFamily  string

	Emails    []ParsedEmail
	Phones    []ParsedPhone
	Addresses []ParsedAddress
	URLs      []ParsedURL
	IMPPs     []ParsedIMPP

	Org      string
	Title    string
	Note     string
	Bday     string
	Nickname string

	Categories []string

	// Photo fields (Phase 2b.2.b.2). Flat-scalar pattern matching Org/Title.
	// At most one of {PhotoData + PhotoMediaType} OR PhotoURL is populated.
	PhotoData      string // base64-encoded image bytes (inline embed)
	PhotoMediaType string // e.g. "image/jpeg"
	PhotoURL       string // vCard PHOTO URL-ref (not fetched in this phase)
}

// ParsedEmail is one EMAIL property on a vCard, with its first TYPE param.
type ParsedEmail struct {
	Value     string
	Type      string
	IsPrimary bool
}

// ParsedPhone is one TEL property.
type ParsedPhone struct {
	Value     string
	Type      string
	IsPrimary bool
}

// ParsedAddress is one ADR property, with structured parts.
type ParsedAddress struct {
	Type     string
	Street   string
	City     string
	Region   string
	Postcode string
	Country  string
}

// ParsedURL is one URL property.
type ParsedURL struct {
	Value string
	Type  string
}

// ParsedIMPP is one IMPP (instant-messaging) property.
type ParsedIMPP struct {
	Handle string
	Type   string
}

// parseVCard returns one ParsedRecord per vCard (no per-email fan-out).
// Returns nil when the address object has no Card data.
func parseVCard(obj carddav.AddressObject) *ParsedRecord {
	if obj.Card == nil {
		return nil
	}
	card := obj.Card

	rec := &ParsedRecord{
		Href: obj.Path,
		ETag: obj.ETag,
	}

	// FN + N
	if fn := card.PreferredValue(vcard.FieldFormattedName); fn != "" {
		rec.FN = strings.TrimSpace(fn)
	}
	if n := card.Name(); n != nil {
		rec.NGiven = strings.TrimSpace(n.GivenName)
		rec.NFamily = strings.TrimSpace(n.FamilyName)
		if rec.FN == "" {
			parts := []string{}
			if rec.NGiven != "" {
				parts = append(parts, rec.NGiven)
			}
			if rec.NFamily != "" {
				parts = append(parts, rec.NFamily)
			}
			rec.FN = strings.Join(parts, " ")
		}
	}

	// EMAIL (multi)
	for i, f := range card[vcard.FieldEmail] {
		val := strings.TrimSpace(f.Value)
		if val == "" {
			continue
		}
		rec.Emails = append(rec.Emails, ParsedEmail{
			Value:     val,
			Type:      firstFieldType(f),
			IsPrimary: i == 0,
		})
	}

	// TEL (multi)
	for i, f := range card[vcard.FieldTelephone] {
		val := strings.TrimSpace(f.Value)
		if val == "" {
			continue
		}
		rec.Phones = append(rec.Phones, ParsedPhone{
			Value:     val,
			Type:      firstFieldType(f),
			IsPrimary: i == 0,
		})
	}

	// ADR (multi, structured)
	for _, addr := range card.Addresses() {
		t := ""
		if addr.Field != nil {
			t = firstFieldType(addr.Field)
		}
		rec.Addresses = append(rec.Addresses, ParsedAddress{
			Type:     t,
			Street:   strings.TrimSpace(addr.StreetAddress),
			City:     strings.TrimSpace(addr.Locality),
			Region:   strings.TrimSpace(addr.Region),
			Postcode: strings.TrimSpace(addr.PostalCode),
			Country:  strings.TrimSpace(addr.Country),
		})
	}

	// URL (multi)
	for _, f := range card[vcard.FieldURL] {
		val := strings.TrimSpace(f.Value)
		if val == "" {
			continue
		}
		rec.URLs = append(rec.URLs, ParsedURL{Value: val, Type: firstFieldType(f)})
	}

	// IMPP (multi)
	for _, f := range card[vcard.FieldIMPP] {
		val := strings.TrimSpace(f.Value)
		if val == "" {
			continue
		}
		rec.IMPPs = append(rec.IMPPs, ParsedIMPP{Handle: val, Type: firstFieldType(f)})
	}

	// Single-value scalars.
	rec.Org = strings.TrimSpace(card.PreferredValue(vcard.FieldOrganization))
	rec.Title = strings.TrimSpace(card.PreferredValue(vcard.FieldTitle))
	rec.Note = strings.TrimSpace(card.PreferredValue(vcard.FieldNote))
	rec.Bday = strings.TrimSpace(card.PreferredValue(vcard.FieldBirthday))
	rec.Nickname = strings.TrimSpace(card.PreferredValue(vcard.FieldNickname))

	// CATEGORIES (multi).
	rec.Categories = card.Categories()

	// PHOTO — single-value field with two possible shapes:
	//   - Inline base64: vCard 3.0 dialect is `PHOTO;ENCODING=b;TYPE=JPEG:<base64>`
	//     (the encoding param can be "b", "B", or "base64"; the TYPE is the
	//     image format). vCard 4.0 dialect is a data URI:
	//     `PHOTO:data:image/jpeg;base64,<base64>`.
	//   - URL ref: `PHOTO;VALUE=URI:http://...` (vCard 3) or
	//     `PHOTO:http://...` (vCard 4).
	// We populate PhotoData + PhotoMediaType for inline OR PhotoURL for refs.
	// All-empty = no photo. URL-ref photos are parsed but not fetched in this
	// phase — Avatar falls back to initials with a "(linked from server)" caption.
	if pf := card.Get(vcard.FieldPhoto); pf != nil {
		val := strings.TrimSpace(pf.Value)
		if val != "" {
			isBase64Encoding := false
			if pf.Params != nil {
				enc := strings.ToLower(strings.TrimSpace(pf.Params.Get("ENCODING")))
				if enc == "b" || enc == "base64" {
					isBase64Encoding = true
				}
				// vCard 4.0 uses MEDIATYPE or VALUE=URI rather than ENCODING=b.
				if v := strings.ToLower(strings.TrimSpace(pf.Params.Get("VALUE"))); v == "uri" {
					rec.PhotoURL = val
				}
			}
			if rec.PhotoURL == "" {
				switch {
				case strings.HasPrefix(val, "data:") && strings.Contains(val, ";base64,"):
					// vCard 4.0 data URI: data:image/jpeg;base64,<base64>
					idx := strings.Index(val, ";base64,")
					mediaType := strings.TrimPrefix(val[:idx], "data:")
					rec.PhotoMediaType = strings.TrimSpace(mediaType)
					rec.PhotoData = strings.TrimSpace(val[idx+len(";base64,"):])
				case strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://"):
					// Bare URL value (no VALUE=URI param needed).
					rec.PhotoURL = val
				case isBase64Encoding:
					// vCard 3.0 inline: TYPE param carries the format suffix.
					rec.PhotoData = val
					if pf.Params != nil {
						if t := strings.TrimSpace(pf.Params.Get("TYPE")); t != "" {
							rec.PhotoMediaType = "image/" + strings.ToLower(t)
						}
					}
				}
			}
		}
	}

	// Re-encode the card for vcard_raw round-trip preservation.
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err == nil {
		rec.VCardRaw = buf.String()
	}

	return rec
}

// firstFieldType returns the first TYPE parameter on a Field (lowercased so
// downstream consumers don't deal with HOME vs home variance). Returns "" when
// the field has no TYPE param.
func firstFieldType(f *vcard.Field) string {
	if f == nil || f.Params == nil {
		return ""
	}
	types := f.Params.Types()
	if len(types) == 0 {
		return ""
	}
	return strings.ToLower(types[0])
}

// ErrPreconditionFailed signals a CardDAV PUT/DELETE 412 — the server's
// current ETag for the resource doesn't match the If-Match header we sent.
// Callers (the extension API) re-fetch the server's current state and surface
// a typed conflict event to the UI rather than discarding the user's edit
// silently.
type ErrPreconditionFailed struct {
	Href       string
	ServerETag string // best-effort: server may not send a new ETag with the 412
}

func (e *ErrPreconditionFailed) Error() string {
	return fmt.Sprintf("carddav: precondition failed for %s (server etag: %q)", e.Href, e.ServerETag)
}

// PutContact writes a vCard to the server at href under the given addressbook
// path. If-Match honors the caller-supplied ETag (exact match — 412 on
// mismatch). Returns the server's new ETag from the response (best-effort —
// returns "" if the server doesn't echo one; the next sync will pick it up).
//
// addressbookPath should be the addressbook's path relative to the base URL
// (the same value stored on contact_source_addressbooks.path). href is the
// full vCard resource path (typically "<addressbookPath>/<uuid>.vcf").
//
// Reuses the existing httpClient + basic-auth wrapping established at client
// construction so xmlFixTransport normalization applies to any error-body
// XML, and basic auth flows through automatically.
func (c *Client) PutContact(addressbookPath, href, ifMatchETag string, ifNoneMatchAny bool, card []byte) (string, error) {
	fullURL := resolveURL(c.baseURL, href)
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)

	req, err := http.NewRequest(http.MethodPut, fullURL, bytes.NewReader(card))
	if err != nil {
		return "", fmt.Errorf("build PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "text/vcard; charset=utf-8")
	// Create-not-update semantics (ifNoneMatchAny) and update-with-exact-etag
	// semantics (ifMatchETag) are mutually exclusive — callers should pass one
	// or neither, never both. If both are set, If-Match wins to match the
	// historical update behavior.
	if ifMatchETag != "" {
		req.Header.Set("If-Match", quotedETag(ifMatchETag))
	} else if ifNoneMatchAny {
		req.Header.Set("If-None-Match", "*")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT %s: %w", fullURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPreconditionFailed {
		return "", &ErrPreconditionFailed{Href: href, ServerETag: resp.Header.Get("ETag")}
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("PUT %s: unexpected status %d: %s", fullURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return strings.Trim(resp.Header.Get("ETag"), `"`), nil
}

// DeleteContact removes a vCard from the server at href. If-Match honors
// the caller-supplied ETag (exact match — 412 on mismatch). 204 / 200 / 404
// all count as success (404 is idempotent — the resource is gone either way).
func (c *Client) DeleteContact(addressbookPath, href, ifMatchETag string) error {
	fullURL := resolveURL(c.baseURL, href)
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)

	req, err := http.NewRequest(http.MethodDelete, fullURL, nil)
	if err != nil {
		return fmt.Errorf("build DELETE request: %w", err)
	}
	if ifMatchETag != "" {
		req.Header.Set("If-Match", quotedETag(ifMatchETag))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", fullURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPreconditionFailed {
		return &ErrPreconditionFailed{Href: href, ServerETag: resp.Header.Get("ETag")}
	}
	if resp.StatusCode == http.StatusNotFound {
		// Already gone — treat as success (idempotent).
		return nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("DELETE %s: unexpected status %d: %s", fullURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// FetchContactByPath fetches a single vCard from the server by its href via
// addressbook-multiget. Used by the 412-recovery path: after a precondition
// failure we re-fetch the server's current state, sync locally, and surface
// the conflict to the UI.
func (c *Client) FetchContactByPath(addressbookPath, href string) (*ParsedRecord, error) {
	fullPath := resolveURL(c.baseURL, addressbookPath)
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)
	abClient, err := carddav.NewClient(httpClient, fullPath)
	if err != nil {
		return nil, fmt.Errorf("build addressbook client: %w", err)
	}
	records, err := c.fetchContactsByPath(abClient, addressbookPath, []string{href})
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// quotedETag ensures the ETag value is wrapped in literal quotes, the form
// required by RFC 7232 If-Match. Strips any existing surrounding quotes first
// so re-quoting is idempotent.
func quotedETag(etag string) string {
	etag = strings.TrimSpace(etag)
	etag = strings.Trim(etag, `"`)
	return `"` + etag + `"`
}

// SyncResult represents the result of an incremental sync
type SyncResult struct {
	SyncToken string          // New sync token to store
	Updated   []*ParsedRecord // Records that were added/modified (one entry per vCard)
	Deleted   []string        // Hrefs of records that were deleted
}

// SyncAddressbook performs an incremental sync using sync-collection
// If syncToken is empty, it performs a full sync
// Returns the new sync token and the changes since the last sync
func (c *Client) SyncAddressbook(addressbookPath, syncToken string) (*SyncResult, error) {
	ctx := context.Background()
	c.log.Debug().
		Str("path", addressbookPath).
		Str("syncToken", syncToken).
		Msg("Starting sync-collection")

	// Resolve the addressbook path against the base URL
	fullPath := resolveURL(c.baseURL, addressbookPath)

	// Create a new client for this specific addressbook
	httpClient := webdav.HTTPClientWithBasicAuth(
		newHTTPClient(60*time.Second),
		c.username, c.password,
	)

	abClient, err := carddav.NewClient(httpClient, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create client for addressbook: %w", err)
	}

	// Perform sync-collection request
	query := &carddav.SyncQuery{
		DataRequest: carddav.AddressDataRequest{
			AllProp: true, // Request full vCard data
		},
		SyncToken: syncToken,
	}

	syncResp, err := abClient.SyncCollection(ctx, addressbookPath, query)
	if err != nil {
		// If sync-collection fails (e.g., invalid token), return error
		// Caller should fall back to full sync
		return nil, fmt.Errorf("sync-collection failed: %w", err)
	}

	c.log.Debug().
		Int("updated", len(syncResp.Updated)).
		Int("deleted", len(syncResp.Deleted)).
		Str("newToken", syncResp.SyncToken).
		Msg("Sync-collection completed")

	result := &SyncResult{
		SyncToken: syncResp.SyncToken,
		Deleted:   syncResp.Deleted,
	}

	// If we have updated items, we need to fetch their full vCard data
	// The sync-collection response may not include full card data
	if len(syncResp.Updated) > 0 {
		// Check if the response includes card data
		hasCardData := false
		for _, obj := range syncResp.Updated {
			if obj.Card != nil && len(obj.Card) > 0 {
				hasCardData = true
				break
			}
		}

		if hasCardData {
			// Parse records directly from sync response.
			for _, obj := range syncResp.Updated {
				parsed := parseVCard(obj)
				if parsed == nil {
					continue
				}
				result.Updated = append(result.Updated, parsed)
			}
		}
		if !hasCardData {
			// Need to fetch full card data using multiget.
			paths := make([]string, len(syncResp.Updated))
			for i, obj := range syncResp.Updated {
				paths[i] = obj.Path
			}

			records, err := c.fetchContactsByPath(abClient, addressbookPath, paths)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch updated records: %w", err)
			}
			result.Updated = records
		}
	}

	c.log.Info().
		Int("updated", len(result.Updated)).
		Int("deleted", len(result.Deleted)).
		Str("path", addressbookPath).
		Msg("Incremental sync completed")

	return result, nil
}

// fetchContactsByPath fetches records by their paths using addressbook-multiget.
// Returns one ParsedRecord per vCard.
func (c *Client) fetchContactsByPath(client *carddav.Client, addressbookPath string, paths []string) ([]*ParsedRecord, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	ctx := context.Background()
	c.log.Debug().
		Int("count", len(paths)).
		Msg("Fetching records by path using multiget")

	multiGet := &carddav.AddressBookMultiGet{
		Paths: paths,
		DataRequest: carddav.AddressDataRequest{
			AllProp: true,
		},
	}

	addressObjects, err := client.MultiGetAddressBook(ctx, addressbookPath, multiGet)
	if err != nil {
		return nil, fmt.Errorf("multiget failed: %w", err)
	}

	records := make([]*ParsedRecord, 0, len(addressObjects))
	for _, obj := range addressObjects {
		parsed := parseVCard(obj)
		if parsed == nil {
			continue
		}
		records = append(records, parsed)
	}
	return records, nil
}

// TestConnection tests the connection to the CardDAV server
func TestConnection(baseURL, username, password string) error {
	log := logging.WithComponent("carddav-test")
	log.Info().Str("url", baseURL).Msg("Testing CardDAV connection")

	// Try to discover addressbooks - this validates credentials and connectivity
	addressbooks, err := DiscoverAddressbooks(baseURL, username, password)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}

	if len(addressbooks) == 0 {
		return fmt.Errorf("connection successful but no addressbooks found")
	}

	log.Info().Int("addressbooks", len(addressbooks)).Msg("Connection test successful")
	return nil
}
