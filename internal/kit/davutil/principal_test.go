package davutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Reporter's exact multistatus body from issue #363 (SabreDAV/Davis).
const davisPrincipalBody = `<?xml version="1.0"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/dav/</d:href>
    <d:propstat>
      <d:prop><d:current-user-principal><d:href>/dav/principals/someuser/</d:href></d:current-user-principal></d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`

// strictSlashServer replicates #363: the collection answers only at /dav/
// (with trailing slash); /dav is a 404.
func strictSlashServer() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dav/" || r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(davisPrincipalBody)) //nolint:errcheck
	})
}

func TestFindCurrentUserPrincipalRaw(t *testing.T) {
	srv := httptest.NewServer(strictSlashServer())
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	tests := []struct {
		name string
		url  string
	}{
		{"trailing slash preserved", srv.URL + "/dav/"},
		{"missing slash retried with slash", srv.URL + "/dav"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal, err := FindCurrentUserPrincipalRaw(context.Background(), client, tt.url)
			if err != nil {
				t.Fatalf("FindCurrentUserPrincipalRaw(%s): %v", tt.url, err)
			}
			if principal != "/dav/principals/someuser/" {
				t.Errorf("principal = %q, want /dav/principals/someuser/", principal)
			}
		})
	}
}

func TestFindCurrentUserPrincipalRaw_Errors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/html/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>login</html>")) //nolint:errcheck
	})
	mux.HandleFunc("/garbage/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte("not xml at all")) //nolint:errcheck
	})
	mux.HandleFunc("/empty/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"></d:multistatus>`)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	for _, path := range []string{"/html/", "/garbage/", "/empty/"} {
		if _, err := FindCurrentUserPrincipalRaw(context.Background(), client, srv.URL+path); err == nil {
			t.Errorf("probe of %s succeeded, want error", path)
		}
	}
}
