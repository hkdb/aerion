package compose

import (
	"net/url"
	"strings"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

// Launcher is the slim interface this package needs from the app layer. The
// concrete *app.App satisfies it via its OpenComposerWindow method. Defining
// the interface here avoids an app→compose→app import cycle.
type Launcher interface {
	OpenComposerWindow(accountID, mode, messageID, draftID, mailtoURL string) error
}

// API implements coreapi.Composer by translating a ComposeRequest into a
// mailto URL and handing it to the Launcher.
type API struct {
	launcher Launcher
}

// NewAPI constructs the Composer API wrapper.
func NewAPI(l Launcher) *API {
	return &API{launcher: l}
}

// OpenComposer prefills a new composer window from a ComposeRequest. Phase 1
// support is the common path (To/Cc/Bcc/Subject/Body). Attachments and
// ReplyTo require deeper integration with the composer state and are deferred
// to Phase 2+.
func (a *API) OpenComposer(req coreapi.ComposeRequest) error {
	if len(req.Attachments) > 0 {
		return coreapi.ErrUnimplemented
	}
	if req.ReplyTo != nil {
		return coreapi.ErrUnimplemented
	}
	mailto := buildMailtoURL(req)
	return a.launcher.OpenComposerWindow(req.AccountID, "new", "", "", mailto)
}

// buildMailtoURL composes an RFC 6068 mailto URL from the request's address
// and body fields. Used as the wire format between this package and the
// existing composer-window launcher.
func buildMailtoURL(req coreapi.ComposeRequest) string {
	var b strings.Builder
	b.WriteString("mailto:")
	b.WriteString(joinAddresses(req.To))

	params := url.Values{}
	if cc := joinAddresses(req.Cc); cc != "" {
		params.Set("cc", cc)
	}
	if bcc := joinAddresses(req.Bcc); bcc != "" {
		params.Set("bcc", bcc)
	}
	if req.Subject != "" {
		params.Set("subject", req.Subject)
	}
	if req.Body != "" {
		params.Set("body", req.Body)
	}
	if encoded := params.Encode(); encoded != "" {
		b.WriteByte('?')
		b.WriteString(encoded)
	}
	return b.String()
}

func joinAddresses(addrs []coreapi.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if a.Email == "" {
			continue
		}
		parts = append(parts, a.Email)
	}
	return strings.Join(parts, ",")
}
