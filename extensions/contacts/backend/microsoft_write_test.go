package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestMicrosoftWriter(srv *httptest.Server) *MicrosoftContactsWriter {
	w := NewMicrosoftContactsWriter(srv.Client())
	w.apiBase = srv.URL + "/v1.0"
	return w
}

func TestMicrosoftWriter_CreateContact_DefaultFolder(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody msContact
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(msContact{
			ID:          "msft-id-1",
			ETag:        "W/\"abc\"",
			DisplayName: gotBody.DisplayName,
		})
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	got, err := writer.CreateContact(context.Background(), "", &msContact{
		DisplayName: "Alice",
		GivenName:   "Alice",
		EmailAddresses: []msEmailAddress{
			{Address: "alice@example.com", Name: "work"},
		},
		// ID + ETag should be stripped before sending.
		ID:   "should-be-stripped",
		ETag: "should-be-stripped",
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/v1.0/me/contacts" {
		t.Errorf("path: got %q, want /v1.0/me/contacts (default folder)", gotPath)
	}
	if gotBody.ID != "" || gotBody.ETag != "" {
		t.Errorf("request body must strip server-assigned fields, got %+v", gotBody)
	}
	if got.ID != "msft-id-1" {
		t.Errorf("returned ID: got %q", got.ID)
	}
}

func TestMicrosoftWriter_CreateContact_NamedFolder(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(msContact{ID: "x"})
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	_, err := writer.CreateContact(context.Background(), "folder-abc", &msContact{DisplayName: "Bob"})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if gotPath != "/v1.0/me/contactFolders/folder-abc/contacts" {
		t.Errorf("path: got %q", gotPath)
	}
}

func TestMicrosoftWriter_UpdateContact(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(msContact{ID: "contact-1", DisplayName: "Updated"})
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	got, err := writer.UpdateContact(context.Background(), "contact-1", &msContact{DisplayName: "Updated"})
	if err != nil {
		t.Fatalf("UpdateContact: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method: got %q", gotMethod)
	}
	if gotPath != "/v1.0/me/contacts/contact-1" {
		t.Errorf("path: got %q", gotPath)
	}
	if got.DisplayName != "Updated" {
		t.Errorf("got %+v", got)
	}
}

func TestMicrosoftWriter_DeleteContact(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	if err := writer.DeleteContact(context.Background(), "contact-1"); err != nil {
		t.Fatalf("DeleteContact: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method: got %q", gotMethod)
	}
	if gotPath != "/v1.0/me/contacts/contact-1" {
		t.Errorf("path: got %q", gotPath)
	}
}

func TestMicrosoftWriter_UpdatePhoto(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	err := writer.UpdatePhoto(context.Background(), "contact-1", []byte{0xFF, 0xD8, 0xFF, 0xE0})
	if err != nil {
		t.Fatalf("UpdatePhoto: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method: got %q", gotMethod)
	}
	if gotPath != "/v1.0/me/contacts/contact-1/photo/$value" {
		t.Errorf("path: got %q", gotPath)
	}
	if gotCT != "image/jpeg" {
		t.Errorf("content-type: got %q", gotCT)
	}
	if len(gotBody) != 4 {
		t.Errorf("body length: got %d, want 4", len(gotBody))
	}
}

func TestMicrosoftWriter_RateLimitRetryAfter(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(msContact{ID: "x"})
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	start := time.Now()
	_, err := writer.CreateContact(context.Background(), "", &msContact{DisplayName: "X"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if hits != 2 {
		t.Errorf("expected 2 hits, got %d", hits)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected to honor Retry-After: 1, elapsed=%v", elapsed)
	}
}

func TestMicrosoftWriter_GivesUpAfterSecondRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	_, err := writer.CreateContact(context.Background(), "", &msContact{DisplayName: "X"})
	if err == nil || !strings.Contains(err.Error(), "rate-limited after retry") {
		t.Fatalf("expected rate-limited error, got %v", err)
	}
}

func TestMicrosoftWriter_ErrorClassification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"BadRequest","message":"invalid request shape"}}`)
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	_, err := writer.CreateContact(context.Background(), "", &msContact{DisplayName: "X"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "BadRequest") || !strings.Contains(err.Error(), "invalid request shape") {
		t.Errorf("error message should carry Graph code + msg: %v", err)
	}
}

func TestMicrosoftWriter_ListContactFolders_Paginates(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			_ = json.NewEncoder(w).Encode(msContactFoldersResponse{
				Value: []msContactFolder{
					{ID: "f1", DisplayName: "Friends"},
				},
				NextLink: "/v1.0/me/contactFolders?$skiptoken=next",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(msContactFoldersResponse{
			Value: []msContactFolder{
				{ID: "f2", DisplayName: "Work"},
			},
		})
	}))
	defer srv.Close()

	writer := newTestMicrosoftWriter(srv)
	// NextLink in the response is the SERVER's full URL — we forward it
	// verbatim, so the test setup constructs a URL relative to the test
	// server's base. Override NextLink in resp 1 to be absolute against the
	// test server.
	srvHandler := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/me/contactFolders" && r.URL.RawQuery == "" {
			_ = json.NewEncoder(w).Encode(msContactFoldersResponse{
				Value:    []msContactFolder{{ID: "f1", DisplayName: "Friends"}},
				NextLink: srv.URL + "/v1.0/me/contactFolders?$skiptoken=next",
			})
			return
		}
		if r.URL.Query().Get("$skiptoken") == "next" {
			_ = json.NewEncoder(w).Encode(msContactFoldersResponse{
				Value: []msContactFolder{{ID: "f2", DisplayName: "Work"}},
			})
			return
		}
		srvHandler.ServeHTTP(w, r)
	})

	folders, err := writer.ListContactFolders(context.Background())
	if err != nil {
		t.Fatalf("ListContactFolders: %v", err)
	}
	if len(folders) != 2 {
		t.Fatalf("got %d folders, want 2", len(folders))
	}
	if folders[0].ID != "f1" || folders[1].ID != "f2" {
		t.Errorf("folder order/ids wrong: %+v", folders)
	}
}
