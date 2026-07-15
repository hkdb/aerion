package contact

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
)

// maxInlinePhotoBytes bounds how large a synced contact photo may be before we
// store it inline. Avatar-sized JPEGs are a few–tens of KB; anything much larger
// would bloat the DB and the Wails bridge payload for no visible benefit, so we
// skip it (the avatar falls back to the colored circle).
const maxInlinePhotoBytes = 256 * 1024

// fetchInlinePhoto executes req (already built + authorized by the caller),
// downloads an image body, and returns it base64-encoded with its media type.
//
// It is deliberately best-effort: any non-200 status (including the common 404
// "contact has no photo"), an over-cap body, or any transport/read error yields
// ok=false so the caller simply leaves the record photo-less. It never returns
// an error — a photo failure must never break a contact sync.
func fetchInlinePhoto(client *http.Client, req *http.Request, maxBytes int64) (data, mediaType string, ok bool) {
	resp, err := client.Do(req)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", false
	}

	// Read one byte past the cap so we can tell "exactly at cap" from "over cap".
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(body) == 0 || int64(len(body)) > maxBytes {
		return "", "", false
	}

	mediaType = strings.TrimSpace(resp.Header.Get("Content-Type"))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i]) // drop "; charset=..." etc.
	}
	if !strings.HasPrefix(mediaType, "image/") {
		mediaType = "image/jpeg" // sane default; providers occasionally omit/mislabel
	}

	return base64.StdEncoding.EncodeToString(body), mediaType, true
}
