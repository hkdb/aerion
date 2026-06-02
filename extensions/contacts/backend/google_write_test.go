package backend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestWriter(srv *httptest.Server) *GoogleContactsWriter {
	w := NewGoogleContactsWriter(srv.Client())
	w.apiBase = srv.URL + "/v1"
	return w
}

func TestGoogleWriter_CreateContact_Happy(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody googlePerson
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(googlePerson{
			ResourceName: "people/c123",
			Metadata: &googlePersonMetadata{
				Sources: []googlePersonSource{{Type: "CONTACT", ID: "src", ETag: "ETAG-1"}},
			},
			Names:          gotBody.Names,
			EmailAddresses: gotBody.EmailAddresses,
		})
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	person := &googlePerson{
		Names:          []googleName{{DisplayName: "Alice", GivenName: "Alice"}},
		EmailAddresses: []googleEmail{{Value: "alice@example.com"}},
		// Memberships should be stripped before send.
		Memberships:  []googleMembership{{ContactGroupMembership: &googleContactGroupMembership{ContactGroupResourceName: "contactGroups/foo"}}},
		ResourceName: "should-be-stripped",
		ETag:         "should-be-stripped",
	}

	got, err := writer.CreateContact(context.Background(), person)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/v1/people:createContact" {
		t.Errorf("path: got %q, want /v1/people:createContact", gotPath)
	}
	if len(gotBody.Memberships) != 0 {
		t.Errorf("createContact must strip memberships, got %v", gotBody.Memberships)
	}
	if gotBody.ResourceName != "" || gotBody.ETag != "" {
		t.Errorf("createContact must strip resourceName/etag, got %+v", gotBody)
	}
	if got.ResourceName != "people/c123" {
		t.Errorf("resourceName: got %q", got.ResourceName)
	}
	if etagFromPerson(got) != "ETAG-1" {
		t.Errorf("etag: got %q, want ETAG-1", etagFromPerson(got))
	}
}

func TestGoogleWriter_UpdateContact_StampsEtagAndMask(t *testing.T) {
	var gotMethod, gotPath, gotMask string
	var gotBody googlePerson
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotMask = r.URL.Query().Get("updatePersonFields")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(googlePerson{
			ResourceName: "people/c123",
			Metadata: &googlePersonMetadata{
				Sources: []googlePersonSource{{Type: "CONTACT", ETag: "ETAG-2"}},
			},
		})
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	_, err := writer.UpdateContact(context.Background(), "people/c123", "ETAG-1", &googlePerson{
		Names: []googleName{{DisplayName: "Alice"}},
	}, "names,emailAddresses")
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method: got %q, want PATCH", gotMethod)
	}
	if gotPath != "/v1/people/c123:updateContact" {
		t.Errorf("path: got %q", gotPath)
	}
	if gotMask != "names,emailAddresses" {
		t.Errorf("mask: got %q", gotMask)
	}
	if gotBody.Metadata == nil || len(gotBody.Metadata.Sources) == 0 {
		t.Fatalf("metadata.sources missing in request body")
	}
	if gotBody.Metadata.Sources[0].ETag != "ETAG-1" {
		t.Errorf("etag in body: got %q, want ETAG-1", gotBody.Metadata.Sources[0].ETag)
	}
}

func TestGoogleWriter_UpdateContact_EtagMismatchTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"etag mismatch","status":"FAILED_PRECONDITION"}}`)
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	_, err := writer.UpdateContact(context.Background(), "people/c123", "stale-etag", &googlePerson{}, "names")
	if err == nil {
		t.Fatal("expected ErrGoogleEtagMismatch, got nil")
	}
	var typed *ErrGoogleEtagMismatch
	if !errors.As(err, &typed) {
		t.Fatalf("expected *ErrGoogleEtagMismatch, got %T: %v", err, err)
	}
	if typed.ResourceName != "people/c123" {
		t.Errorf("resourceName: got %q", typed.ResourceName)
	}
}

func TestGoogleWriter_DeleteContact_OK(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	if err := writer.DeleteContact(context.Background(), "people/c123"); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method: got %q", gotMethod)
	}
	if gotPath != "/v1/people/c123:deleteContact" {
		t.Errorf("path: got %q", gotPath)
	}
}

func TestGoogleWriter_RateLimitRetryAfter(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(googlePerson{ResourceName: "people/c123"})
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	start := time.Now()
	_, err := writer.CreateContact(context.Background(), &googlePerson{Names: []googleName{{DisplayName: "X"}}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if hits != 2 {
		t.Errorf("expected 2 hits (one 429 + one success), got %d", hits)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected to honor Retry-After: 1, elapsed=%v", elapsed)
	}
}

func TestGoogleWriter_RateLimitGivesUpAfterSecondAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	_, err := writer.CreateContact(context.Background(), &googlePerson{Names: []googleName{{DisplayName: "X"}}})
	if err == nil || !strings.Contains(err.Error(), "rate-limited after retry") {
		t.Fatalf("expected rate-limited error, got %v", err)
	}
}

func TestGoogleWriter_ListContactGroups_Paginates(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			_ = json.NewEncoder(w).Encode(googleContactGroupsResponse{
				ContactGroups: []googleContactGroup{
					{ResourceName: "contactGroups/myContacts", GroupType: "SYSTEM_CONTACT_GROUP", FormattedName: "My Contacts"},
					{ResourceName: "contactGroups/g1", GroupType: "USER_CONTACT_GROUP", FormattedName: "Friends"},
				},
				NextPageToken: "tok2",
			})
			return
		}
		if r.URL.Query().Get("pageToken") != "tok2" {
			t.Errorf("missing pageToken on second hit: %v", r.URL.Query())
		}
		_ = json.NewEncoder(w).Encode(googleContactGroupsResponse{
			ContactGroups: []googleContactGroup{
				{ResourceName: "contactGroups/g2", GroupType: "USER_CONTACT_GROUP", FormattedName: "Family"},
			},
		})
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	groups, err := writer.ListContactGroups(context.Background())
	if err != nil {
		t.Fatalf("ListContactGroups: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}
}

func TestGoogleWriter_ModifyGroupMembership_BodyShape(t *testing.T) {
	var gotBody googleModifyMembersRequest
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	err := writer.ModifyGroupMembership(context.Background(), "contactGroups/g1", []string{"people/c1"}, nil)
	if err != nil {
		t.Fatalf("ModifyGroupMembership: %v", err)
	}
	if gotPath != "/v1/contactGroups/g1/members:modify" {
		t.Errorf("path: got %q", gotPath)
	}
	if len(gotBody.ResourceNamesToAdd) != 1 || gotBody.ResourceNamesToAdd[0] != "people/c1" {
		t.Errorf("add list: got %v", gotBody.ResourceNamesToAdd)
	}
}

func TestGoogleWriter_ModifyGroupMembership_NoopOnEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called on empty add/remove")
	}))
	defer srv.Close()

	writer := newTestWriter(srv)
	if err := writer.ModifyGroupMembership(context.Background(), "contactGroups/g1", nil, nil); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"5", 5 * time.Second},
		{"garbage", 0},
	}
	for _, tc := range tests {
		got := parseRetryAfter(tc.in)
		if got != tc.want {
			t.Errorf("parseRetryAfter(%q): got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestExtractResourceName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://example.com/v1/people/c123:updateContact?x=y", "people/c123"},
		{"https://example.com/v1/people/c123:deleteContact", "people/c123"},
		{"https://example.com/v1/people:createContact", ""}, // matches "people:createContact" path[1] is verb-ish — verify
		{"https://example.com/v1/contactGroups/g1/members:modify", "contactGroups/g1"},
	}
	for _, tc := range tests {
		got := extractResourceName(tc.in)
		if got != tc.want {
			t.Errorf("extractResourceName(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}
