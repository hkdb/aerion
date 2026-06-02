package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// googleAPIBase is the People API root. Overridden in tests via the
// `apiBase` field on GoogleContactsWriter.
const googleAPIBase = "https://people.googleapis.com/v1"

// googlePersonFields is the field mask used on read-back operations
// (createContact response, updateContact response, GET-for-etag-refresh).
// Mirrors the write-side mappings in google_convert.go.
const googlePersonFields = "names,emailAddresses,phoneNumbers,addresses,urls,imClients,organizations,biographies,birthdays,nicknames,memberships,metadata,photos"

// GoogleContactsWriter is the Phase 2b.3 write-side client for Google People
// API. Built fresh per call (or close to it) by the extension's api.go using
// an *http.Client provided by the auth broker — extension code never sees the
// access token directly.
//
// Decoupled from internal/contact/google_sync.go on purpose: the READ-side
// types in core are minimal (resourceName + names + emails) and don't carry
// the full Person field set the write side needs (phones, addresses, photos,
// memberships, metadata.sources for etag). Re-implementing the shape here
// keeps write CRUD in one place and respects the host/extension boundary.
type GoogleContactsWriter struct {
	httpClient *http.Client
	log        zerolog.Logger
	// apiBase overrides googleAPIBase for tests (httptest.Server URL).
	apiBase string
}

// NewGoogleContactsWriter constructs a writer with the given authenticated
// HTTP client. Pass an *http.Client from coreapi.Auth().HTTPClient(...) — its
// transport injects the bearer token + refreshes on 401 transparently.
func NewGoogleContactsWriter(httpClient *http.Client) *GoogleContactsWriter {
	return &GoogleContactsWriter{
		httpClient: httpClient,
		log:        logging.WithComponent("google-contacts-write"),
		apiBase:    googleAPIBase,
	}
}

// ErrGoogleEtagMismatch is returned when Google rejects a PATCH because the
// supplied etag doesn't match the server's current version. Translated by the
// API layer into *coreapi.ErrConflict so the existing Bridge.emitConflict
// path fires the `contacts:conflict` Wails event.
type ErrGoogleEtagMismatch struct {
	ResourceName string
	Message      string
}

func (e *ErrGoogleEtagMismatch) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("google people: etag mismatch on %s", e.ResourceName)
	}
	return fmt.Sprintf("google people: etag mismatch on %s: %s", e.ResourceName, e.Message)
}

// ----- API shapes ------------------------------------------------------------

// googlePerson is the write-shape for the People API Person resource. Fields
// that aren't currently surfaced by contact.Record are omitted; if Aerion
// adds them later, extend here and in google_convert.go.
type googlePerson struct {
	ResourceName   string                `json:"resourceName,omitempty"`
	ETag           string                `json:"etag,omitempty"`
	Metadata       *googlePersonMetadata `json:"metadata,omitempty"`
	Names          []googleName          `json:"names,omitempty"`
	EmailAddresses []googleEmail         `json:"emailAddresses,omitempty"`
	PhoneNumbers   []googlePhone         `json:"phoneNumbers,omitempty"`
	Addresses      []googleAddress       `json:"addresses,omitempty"`
	URLs           []googleURL           `json:"urls,omitempty"`
	IMClients      []googleIMClient      `json:"imClients,omitempty"`
	Organizations  []googleOrganization  `json:"organizations,omitempty"`
	Biographies    []googleBiography     `json:"biographies,omitempty"`
	Birthdays      []googleBirthday      `json:"birthdays,omitempty"`
	Nicknames      []googleNickname      `json:"nicknames,omitempty"`
	Memberships    []googleMembership    `json:"memberships,omitempty"`
	Photos         []googlePhoto         `json:"photos,omitempty"`
}

type googlePersonMetadata struct {
	Sources []googlePersonSource `json:"sources,omitempty"`
}

type googlePersonSource struct {
	Type string `json:"type,omitempty"` // "CONTACT" for user-managed contacts
	ID   string `json:"id,omitempty"`
	ETag string `json:"etag,omitempty"`
}

type googleName struct {
	GivenName   string `json:"givenName,omitempty"`
	FamilyName  string `json:"familyName,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type googleEmail struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

type googlePhone struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

type googleAddress struct {
	StreetAddress string `json:"streetAddress,omitempty"`
	City          string `json:"city,omitempty"`
	Region        string `json:"region,omitempty"`
	PostalCode    string `json:"postalCode,omitempty"`
	Country       string `json:"country,omitempty"`
	Type          string `json:"type,omitempty"`
}

type googleURL struct {
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

type googleIMClient struct {
	Username string `json:"username,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Type     string `json:"type,omitempty"`
}

type googleOrganization struct {
	Name  string `json:"name,omitempty"`
	Title string `json:"title,omitempty"`
}

type googleBiography struct {
	Value       string `json:"value,omitempty"`
	ContentType string `json:"contentType,omitempty"` // "TEXT_PLAIN" or "TEXT_HTML"
}

type googleBirthday struct {
	Date *googleDate `json:"date,omitempty"`
	Text string      `json:"text,omitempty"`
}

type googleDate struct {
	Year  int `json:"year,omitempty"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

type googleNickname struct {
	Value string `json:"value,omitempty"`
}

type googleMembership struct {
	ContactGroupMembership *googleContactGroupMembership `json:"contactGroupMembership,omitempty"`
}

type googleContactGroupMembership struct {
	ContactGroupResourceName string `json:"contactGroupResourceName,omitempty"`
}

type googlePhoto struct {
	URL     string `json:"url,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// googleContactGroup is the People API ContactGroup resource. Returned by
// ListContactGroups + used by ModifyGroupMembership.
type googleContactGroup struct {
	ResourceName  string `json:"resourceName,omitempty"`
	ETag          string `json:"etag,omitempty"`
	Name          string `json:"name,omitempty"`
	FormattedName string `json:"formattedName,omitempty"`
	GroupType     string `json:"groupType,omitempty"` // "USER_CONTACT_GROUP" | "SYSTEM_CONTACT_GROUP"
}

type googleContactGroupsResponse struct {
	ContactGroups []googleContactGroup `json:"contactGroups,omitempty"`
	NextPageToken string               `json:"nextPageToken,omitempty"`
}

type googleModifyMembersRequest struct {
	ResourceNamesToAdd    []string `json:"resourceNamesToAdd,omitempty"`
	ResourceNamesToRemove []string `json:"resourceNamesToRemove,omitempty"`
}

type googleUpdatePhotoRequest struct {
	PhotoBytes string `json:"photoBytes"` // standard base64 (NOT urlsafe)
	PersonFields string `json:"personFields,omitempty"`
}

type googleUpdatePhotoResponse struct {
	Person *googlePerson `json:"person,omitempty"`
}

type googleAPIErrorBody struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

// ----- HTTP plumbing ---------------------------------------------------------

// doJSON sends a JSON request to Google People API with 429/503 retry-once
// honoring Retry-After. Returns the decoded response body OR a typed error
// for known failure modes (etag mismatch → *ErrGoogleEtagMismatch).
//
// Sequential single-retry only — no exponential backoff loop. The auth broker
// handles 401 refresh transparently; this method just needs to cope with the
// occasional rate limit during interactive Add/Edit/Delete.
func (w *GoogleContactsWriter) doJSON(ctx context.Context, method, urlStr string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("google people: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	// Try, then once-retry on 429/503.
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
		if err != nil {
			return fmt.Errorf("google people: build request: %w", err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		resp, err := w.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("google people: %s %s: %w", method, urlStr, err)
		}

		switch {
		case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent:
			defer resp.Body.Close()
			if out == nil || resp.StatusCode == http.StatusNoContent {
				_, _ = io.Copy(io.Discard, resp.Body)
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("google people: decode response: %w", err)
			}
			return nil

		case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode == http.StatusServiceUnavailable:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			if attempt == 1 {
				return fmt.Errorf("google people: rate-limited after retry (status %d)", resp.StatusCode)
			}
			if retryAfter > 0 {
				w.log.Warn().Dur("retry_after", retryAfter).Int("status", resp.StatusCode).Msg("Google rate-limited; sleeping")
				select {
				case <-time.After(retryAfter):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			// Rebuild body reader for retry (it was consumed).
			if body != nil {
				buf, _ := json.Marshal(body)
				bodyReader = bytes.NewReader(buf)
			}
			continue

		default:
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			return classifyGoogleError(resp.StatusCode, data, extractResourceName(urlStr))
		}
	}
	// Unreachable: loop exits via return/continue.
	return errors.New("google people: doJSON: unreachable")
}

// classifyGoogleError translates an HTTP error response into the right typed
// error. The headline case is 400 `failedPrecondition` (etag mismatch) →
// *ErrGoogleEtagMismatch so the API layer maps it to *coreapi.ErrConflict.
func classifyGoogleError(status int, data []byte, resourceName string) error {
	var body googleAPIErrorBody
	_ = json.Unmarshal(data, &body)

	if status == http.StatusBadRequest && strings.EqualFold(body.Error.Status, "FAILED_PRECONDITION") {
		return &ErrGoogleEtagMismatch{ResourceName: resourceName, Message: body.Error.Message}
	}
	// Surface the structured error message if present, otherwise the raw body
	// — easier debugging than a generic "google people: 400".
	msg := body.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(data))
		if len(msg) > 256 {
			msg = msg[:256] + "..."
		}
	}
	if msg == "" {
		return fmt.Errorf("google people: HTTP %d", status)
	}
	return fmt.Errorf("google people: HTTP %d: %s", status, msg)
}

// parseRetryAfter handles both delta-seconds (e.g., "30") and HTTP-date
// formats. Returns 0 when missing or unparseable (caller can fall back to a
// default backoff).
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

// extractResourceName pulls "people/c12345" out of a URL path so error types
// can reference the targeted resource. Returns empty on no match.
func extractResourceName(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	// Trim the API base prefix from the path. People API paths look like
	// "/v1/people/c12345" or "/v1/people/c12345:updateContactPhoto".
	parts := strings.Split(strings.TrimPrefix(u.Path, "/v1/"), "/")
	if len(parts) < 2 {
		return ""
	}
	// "people/c12345..." — strip a trailing ":verb" if present.
	resourceName := parts[0] + "/" + parts[1]
	if idx := strings.Index(resourceName, ":"); idx > 0 {
		resourceName = resourceName[:idx]
	}
	return resourceName
}

// ----- public methods --------------------------------------------------------

// CreateContact POSTs a new Person and returns the server's view of it
// (which carries the freshly-assigned resourceName + etag + memberships).
//
// Memberships are NOT settable on create (Google rejects them); use
// ModifyGroupMembership immediately after if the contact should land in a
// specific group.
func (w *GoogleContactsWriter) CreateContact(ctx context.Context, person *googlePerson) (*googlePerson, error) {
	if person == nil {
		return nil, errors.New("google people: CreateContact: nil person")
	}
	// Defensive: createContact rejects memberships. Strip before sending.
	clean := *person
	clean.Memberships = nil
	clean.ResourceName = ""
	clean.ETag = ""
	clean.Metadata = nil

	target := w.apiBase + "/people:createContact?personFields=" + url.QueryEscape(googlePersonFields)
	var out googlePerson
	if err := w.doJSON(ctx, http.MethodPost, target, &clean, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateContact PATCHes an existing Person. updatePersonFields IS required by
// the API; pass the FieldMask from fieldMaskForRecord (computed by the convert
// layer based on which fields the patch touches).
//
// The supplied etag is placed at metadata.sources[0].etag (Google requires it
// there, not in an If-Match header). The person's resourceName drives the URL.
//
// On etag mismatch the doJSON layer returns *ErrGoogleEtagMismatch — leave it
// to the API layer to translate into *coreapi.ErrConflict.
func (w *GoogleContactsWriter) UpdateContact(ctx context.Context, resourceName, etag string, person *googlePerson, fieldMask string) (*googlePerson, error) {
	if resourceName == "" {
		return nil, errors.New("google people: UpdateContact: resourceName is required")
	}
	if fieldMask == "" {
		return nil, errors.New("google people: UpdateContact: updatePersonFields mask is required")
	}
	// Defensive copy so we can stamp the etag without mutating the caller's
	// person. Etag goes in metadata.sources[0].etag with type="CONTACT".
	clean := *person
	clean.ResourceName = ""
	clean.ETag = ""
	clean.Metadata = &googlePersonMetadata{
		Sources: []googlePersonSource{{Type: "CONTACT", ETag: etag}},
	}

	q := url.Values{
		"updatePersonFields": {fieldMask},
		"personFields":       {googlePersonFields},
	}
	target := w.apiBase + "/" + resourceName + ":updateContact?" + q.Encode()

	var out googlePerson
	if err := w.doJSON(ctx, http.MethodPatch, target, &clean, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetContact fetches a Person fresh from the server. Used by the API layer to
// refresh an etag after a sync (sync layer doesn't capture etags — see
// store.go's migration 1 comment) before a subsequent UpdateContact.
func (w *GoogleContactsWriter) GetContact(ctx context.Context, resourceName string) (*googlePerson, error) {
	if resourceName == "" {
		return nil, errors.New("google people: GetContact: resourceName is required")
	}
	target := w.apiBase + "/" + resourceName + "?personFields=" + url.QueryEscape(googlePersonFields)
	var out googlePerson
	if err := w.doJSON(ctx, http.MethodGet, target, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteContact removes a contact server-side. Cascade-deletes group
// memberships (per Google's docs). No body, no etag — Google accepts delete
// without a version stamp.
func (w *GoogleContactsWriter) DeleteContact(ctx context.Context, resourceName string) error {
	if resourceName == "" {
		return errors.New("google people: DeleteContact: resourceName is required")
	}
	target := w.apiBase + "/" + resourceName + ":deleteContact"
	return w.doJSON(ctx, http.MethodDelete, target, nil, nil)
}

// UpdatePhoto sets a contact's avatar from the given JPEG/PNG bytes. Returns
// the updated Person (Google's API replies with the full record).
//
// Caller controls the bytes; the API layer typically passes the already-
// resized JPEG produced by Contacts_ResizeContactPhoto. Photo-step failures
// are surfaced as errors but the API layer treats them as non-fatal (the
// contact itself is already saved).
func (w *GoogleContactsWriter) UpdatePhoto(ctx context.Context, resourceName string, photoBytes []byte) (*googlePerson, error) {
	if resourceName == "" {
		return nil, errors.New("google people: UpdatePhoto: resourceName is required")
	}
	if len(photoBytes) == 0 {
		return nil, errors.New("google people: UpdatePhoto: empty photo bytes")
	}
	target := w.apiBase + "/" + resourceName + ":updateContactPhoto"
	body := googleUpdatePhotoRequest{
		PhotoBytes:   base64.StdEncoding.EncodeToString(photoBytes),
		PersonFields: googlePersonFields,
	}
	var out googleUpdatePhotoResponse
	if err := w.doJSON(ctx, http.MethodPatch, target, &body, &out); err != nil {
		return nil, err
	}
	return out.Person, nil
}

// ListContactGroups returns the user's contactGroups. Surfaced by
// listGoogleAddressbooks as pseudo-addressbooks. SYSTEM_CONTACT_GROUP entries
// (myContacts, all, chatBuddies, etc.) are filtered by the API layer — the
// writer returns the full list as-is so other callers can apply different
// policies.
//
// Paginates internally; the API caps at 1000 per page which is plenty for any
// realistic user contact-groups list.
func (w *GoogleContactsWriter) ListContactGroups(ctx context.Context) ([]googleContactGroup, error) {
	var all []googleContactGroup
	pageToken := ""
	for {
		q := url.Values{
			"groupFields": {"name,groupType,formattedName,memberCount"},
			"pageSize":    {"1000"},
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		target := w.apiBase + "/contactGroups?" + q.Encode()
		var resp googleContactGroupsResponse
		if err := w.doJSON(ctx, http.MethodGet, target, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.ContactGroups...)
		if resp.NextPageToken == "" {
			return all, nil
		}
		pageToken = resp.NextPageToken
	}
}

// ModifyGroupMembership adds or removes a contact from a contact group.
// Group resourceName looks like "contactGroups/abc"; the contact resourceNames
// look like "people/c12345". Either add or remove may be empty.
func (w *GoogleContactsWriter) ModifyGroupMembership(ctx context.Context, groupResourceName string, add, remove []string) error {
	if groupResourceName == "" {
		return errors.New("google people: ModifyGroupMembership: groupResourceName is required")
	}
	if len(add) == 0 && len(remove) == 0 {
		return nil // no-op
	}
	target := w.apiBase + "/" + groupResourceName + "/members:modify"
	body := googleModifyMembersRequest{
		ResourceNamesToAdd:    add,
		ResourceNamesToRemove: remove,
	}
	return w.doJSON(ctx, http.MethodPost, target, &body, nil)
}
