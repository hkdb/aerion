package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/hkdb/aerion/internal/message"
)

// recoverFailedHeaderBatch re-fetches the given UIDs without requesting ENVELOPE so the
// go-imap/v2 wire parser can't trip on a malformed envelope from the server. Headers come
// through as raw bytes and are parsed locally via net/mail.
//
// This is the fallback path for the Mailfence-style empty-subject bug where the server
// emits a bare space where a quoted "" or NIL field placeholder should be (issue #209).
// The primary fetch path is unchanged; this only runs when fetchMessageHeaders detects
// the specific envelope parse failure.
func (e *Engine) recoverFailedHeaderBatch(ctx context.Context, client *imapclient.Client, accountID, folderID string, missingUIDs []uint32) ([]*message.Message, error) {
	if len(missingUIDs) == 0 {
		return nil, nil
	}

	e.log.Info().
		Int("count", len(missingUIDs)).
		Str("account", accountID).
		Msg("Recovering header batch without ENVELOPE (envelope parse failed upstream)")

	uidSet := imap.UIDSet{}
	for _, uid := range missingUIDs {
		uidSet.AddNum(imap.UID(uid))
	}

	// Same as the primary fetch options but WITHOUT Envelope. The parser only has to
	// handle Flags/RFC822Size/InternalDate/UID/BODY[HEADER] — none of which are subject
	// to the bare-space malformation that breaks ENVELOPE parsing.
	fetchOptions := &imap.FetchOptions{
		Flags:        true,
		RFC822Size:   true,
		InternalDate: true,
		UID:          true,
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierHeader,
				Peek:      true,
			},
		},
	}

	fetchCmd := client.Fetch(uidSet, fetchOptions)

	var recovered []*message.Message
	for {
		if ctx.Err() != nil {
			fetchCmd.Close()
			return recovered, ctx.Err()
		}

		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		var fetchedUID imap.UID
		var flags []imap.Flag
		var rfc822Size int64
		var headerBytes []byte
		var internalDate time.Time

		for {
			item := msg.Next()
			if item == nil {
				break
			}
			switch data := item.(type) {
			case imapclient.FetchItemDataUID:
				fetchedUID = data.UID
			case imapclient.FetchItemDataFlags:
				flags = data.Flags
			case imapclient.FetchItemDataRFC822Size:
				rfc822Size = data.Size
			case imapclient.FetchItemDataInternalDate:
				internalDate = data.Time
			case imapclient.FetchItemDataBodySection:
				if data.Literal != nil {
					b, rerr := io.ReadAll(data.Literal)
					if rerr != nil {
						e.log.Warn().Err(rerr).Uint32("uid", uint32(fetchedUID)).Msg("Failed to read header literal in recovery")
						continue
					}
					headerBytes = b
				}
			}
		}

		if fetchedUID == 0 {
			continue
		}

		m := &message.Message{
			AccountID:   accountID,
			FolderID:    folderID,
			UID:         uint32(fetchedUID),
			ReceivedAt:  time.Now().UTC(),
			BodyFetched: false,
			Size:        int(rfc822Size),
		}

		if perr := parseHeadersIntoMessage(m, headerBytes); perr != nil {
			e.log.Debug().Err(perr).Uint32("uid", uint32(fetchedUID)).Msg("Header parse failed in recovery, skipping")
			continue
		}

		// Internal date is the IMAP server's record of arrival time; fall back to it if
		// the message has no Date header (rare but possible alongside missing Subject).
		if m.Date.IsZero() && !internalDate.IsZero() {
			m.Date = internalDate.UTC()
		}

		applyFlagsToMessage(m, flags)

		if len(headerBytes) > 0 {
			references := e.extractReferences(headerBytes)
			if len(references) > 0 {
				refsJSON, _ := json.Marshal(references)
				m.References = string(refsJSON)
			}
			m.ReadReceiptTo = e.extractDispositionNotificationTo(headerBytes)
		}

		if err := e.messageStore.Upsert(m); err != nil {
			e.log.Warn().Err(err).Uint32("uid", m.UID).Msg("Failed to save recovered message header")
			continue
		}
		recovered = append(recovered, m)
	}

	if err := fetchCmd.Close(); err != nil {
		e.log.Warn().Err(err).Int("recovered", len(recovered)).Msg("Header recovery fetch close error")
	}

	e.log.Info().
		Int("recovered", len(recovered)).
		Int("requested", len(missingUIDs)).
		Msg("Header recovery complete")

	return recovered, nil
}

// parseHeadersIntoMessage populates message envelope fields from raw RFC 5322 header
// bytes, mirroring what applyEnvelopeToMessage does from an IMAP ENVELOPE. Used by the
// recovery path when the server's ENVELOPE serialization is malformed.
func parseHeadersIntoMessage(m *message.Message, headerBytes []byte) error {
	if len(headerBytes) == 0 {
		return nil
	}

	msg, err := mail.ReadMessage(bytes.NewReader(headerBytes))
	if err != nil {
		return err
	}

	h := msg.Header

	m.Subject = decodeMIMEWord(h.Get("Subject"))
	m.MessageID = strings.Trim(h.Get("Message-ID"), "<>")
	m.InReplyTo = strings.Trim(h.Get("In-Reply-To"), "<>")

	if dateStr := h.Get("Date"); dateStr != "" {
		if t, perr := mail.ParseDate(dateStr); perr == nil {
			m.Date = t.UTC()
		}
	}

	if fromStr := h.Get("From"); fromStr != "" {
		if from, perr := mail.ParseAddress(fromStr); perr == nil {
			m.FromName = decodeMIMEWord(from.Name)
			m.FromEmail = from.Address
		}
	}

	if toStr := h.Get("To"); toStr != "" {
		if to, perr := mail.ParseAddressList(toStr); perr == nil && len(to) > 0 {
			m.ToList = mailAddressListToJSON(to)
		}
	}

	if ccStr := h.Get("Cc"); ccStr != "" {
		if cc, perr := mail.ParseAddressList(ccStr); perr == nil && len(cc) > 0 {
			m.CcList = mailAddressListToJSON(cc)
		}
	}

	if rtStr := h.Get("Reply-To"); rtStr != "" {
		if rt, perr := mail.ParseAddress(rtStr); perr == nil {
			m.ReplyTo = rt.Address
		}
	}

	return nil
}

// mailAddressListToJSON serializes net/mail addresses to the same JSON shape used by
// addressListToJSON for IMAP envelope addresses.
func mailAddressListToJSON(addrs []*mail.Address) string {
	type addr struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}

	list := make([]addr, len(addrs))
	for i, a := range addrs {
		list[i] = addr{
			Name:  decodeMIMEWord(a.Name),
			Email: a.Address,
		}
	}

	data, _ := json.Marshal(list)
	return string(data)
}
