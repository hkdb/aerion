package contact

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fnRT is a RoundTripper backed by a function, so a test can route responses by
// request URL (delta/connections vs. per-photo fetch).
type fnRT func(*http.Request) (*http.Response, error)

func (f fnRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func resp(status int, ctype string, body []byte) *http.Response {
	h := make(http.Header)
	if ctype != "" {
		h.Set("Content-Type", ctype)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestFetchInlinePhoto(t *testing.T) {
	req := func() *http.Request { r, _ := http.NewRequest("GET", "http://x/photo", nil); return r }

	// 200 image → base64 data + media type (charset param stripped).
	okClient := &http.Client{Transport: fnRT(func(*http.Request) (*http.Response, error) {
		return resp(200, "image/png; charset=binary", []byte("PNGDATA")), nil
	})}
	data, mt, ok := fetchInlinePhoto(okClient, req(), maxInlinePhotoBytes)
	if !ok || mt != "image/png" || data != base64.StdEncoding.EncodeToString([]byte("PNGDATA")) {
		t.Fatalf("200 case: ok=%v mt=%q data=%q", ok, mt, data)
	}

	// Missing/non-image content type defaults to image/jpeg.
	jpegClient := &http.Client{Transport: fnRT(func(*http.Request) (*http.Response, error) {
		return resp(200, "", []byte("X")), nil
	})}
	if _, mt, ok := fetchInlinePhoto(jpegClient, req(), maxInlinePhotoBytes); !ok || mt != "image/jpeg" {
		t.Fatalf("default media type: ok=%v mt=%q", ok, mt)
	}

	// 404 (no photo) → not ok.
	notFound := &http.Client{Transport: fnRT(func(*http.Request) (*http.Response, error) {
		return resp(404, "", nil), nil
	})}
	if _, _, ok := fetchInlinePhoto(notFound, req(), maxInlinePhotoBytes); ok {
		t.Fatal("404 must be not ok")
	}

	// Over the cap → not ok.
	big := &http.Client{Transport: fnRT(func(*http.Request) (*http.Response, error) {
		return resp(200, "image/jpeg", bytes.Repeat([]byte("A"), 100)), nil
	})}
	if _, _, ok := fetchInlinePhoto(big, req(), 10); ok {
		t.Fatal("over-cap body must be not ok")
	}
}

func TestGoogleConnToRecord_PicksFirstNonDefaultPhoto(t *testing.T) {
	conn := googleConnection{
		Names: []googleName{{DisplayName: "Al"}},
		Photos: []googlePhoto{
			{URL: "https://sil", Default: true},
			{URL: "https://real", Default: false},
		},
	}
	if rec := googleConnToRecord(conn); rec == nil || rec.PhotoURL != "https://real" {
		t.Fatalf("want PhotoURL https://real, got %+v", rec)
	}

	// Silhouette-only → no photo URL.
	silOnly := googleConnection{Names: []googleName{{DisplayName: "Al"}}, Photos: []googlePhoto{{URL: "https://sil", Default: true}}}
	if rec := googleConnToRecord(silOnly); rec == nil || rec.PhotoURL != "" {
		t.Fatalf("silhouette-only should have empty PhotoURL, got %+v", rec)
	}
}

func TestGoogleSync_EnrichesPhotos(t *testing.T) {
	photoURL := "https://lh3.googleusercontent.com/real"
	body := `{"connections":[` +
		`{"resourceName":"people/A","names":[{"displayName":"Al"}],"emailAddresses":[{"value":"a@x.com"}],"photos":[{"url":"` + photoURL + `","default":false}]},` +
		`{"resourceName":"people/B","names":[{"displayName":"Bo"}],"photos":[{"url":"https://sil","default":true}]}` +
		`],"nextSyncToken":"t"}`

	s := NewGoogleContactsSyncer()
	s.httpClient = &http.Client{Transport: fnRT(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == photoURL {
			return resp(200, "image/jpeg", []byte("JPEGBYTES")), nil
		}
		return resp(200, "application/json", []byte(body)), nil
	})}

	res, err := s.SyncContactsDelta("tok", "")
	if err != nil {
		t.Fatalf("SyncContactsDelta: %v", err)
	}
	al := findRecord(res.Records, "Al")
	if al == nil || al.PhotoData != base64.StdEncoding.EncodeToString([]byte("JPEGBYTES")) || al.PhotoMediaType != "image/jpeg" {
		t.Fatalf("Al photo not enriched: %+v", al)
	}
	bo := findRecord(res.Records, "Bo")
	if bo == nil || bo.PhotoData != "" {
		t.Fatalf("Bo (silhouette-only) should have no photo: %+v", bo)
	}
}

func TestMicrosoftSync_EnrichesPhotos(t *testing.T) {
	body := `{"value":[` +
		`{"id":"A","displayName":"Al","emailAddresses":[{"address":"a@x.com"}]},` +
		`{"id":"B","displayName":"Bo"}` +
		`],"@odata.deltaLink":"https://graph.microsoft.com/v1.0/me/contacts/delta?$deltatoken=x"}`

	s := NewMicrosoftContactsSyncer()
	s.httpClient = &http.Client{Transport: fnRT(func(r *http.Request) (*http.Response, error) {
		u := r.URL.String()
		if strings.Contains(u, "/contacts/A/photo") {
			return resp(200, "image/jpeg", []byte("PIC")), nil
		}
		if strings.Contains(u, "/contacts/B/photo") {
			return resp(404, "", nil), nil // Bo has no photo
		}
		return resp(200, "application/json", []byte(body)), nil
	})}

	res, err := s.SyncContactsDelta("tok", "")
	if err != nil {
		t.Fatalf("SyncContactsDelta: %v", err)
	}
	al := findRecord(res.Records, "Al")
	if al == nil || al.PhotoData != base64.StdEncoding.EncodeToString([]byte("PIC")) || al.PhotoMediaType != "image/jpeg" {
		t.Fatalf("Al photo not enriched: %+v", al)
	}
	bo := findRecord(res.Records, "Bo")
	if bo == nil || bo.PhotoData != "" {
		t.Fatalf("Bo (404 no photo) should have no photo: %+v", bo)
	}
}
