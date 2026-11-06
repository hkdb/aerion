package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hkdb/aerion/internal/logging"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/smtp"
	"github.com/hkdb/aerion/internal/unsubscribe"
)

// ============================================================================
// Unsubscribe API - Exposed to frontend via Wails bindings
// ============================================================================
//
// Mirrors Mailspring's "Unsubscribe" button: the List-Unsubscribe header
// (RFC 2369 / RFC 8058) is preferred, with an "unsubscribe" link in the HTML
// body as a fallback. See internal/unsubscribe for the detection rules.

// headerBackfillTimeout bounds the on-demand IMAP header fetch for messages
// synced before the list_unsubscribe columns existed.
const headerBackfillTimeout = 15 * time.Second

// UnsubscribeResult reports what Unsubscribe actually did.
type UnsubscribeResult struct {
	// Method is the mechanism that succeeded: "one-click", "mailto" or "browser".
	Method unsubscribe.Method `json:"method"`
	// Target is the host or address the request went to.
	Target string `json:"target"`
}

// GetUnsubscribeInfo reports whether a message can be unsubscribed from and
// which mechanism will be used.
func (a *App) GetUnsubscribeInfo(messageID string) (*unsubscribe.Info, error) {
	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}
	return a.detectUnsubscribe(msg)
}

// Unsubscribe performs the unsubscribe request for a message, falling back
// to the next available mechanism when the preferred one fails:
// one-click POST -> mailto -> open the URL in the browser.
func (a *App) Unsubscribe(messageID string) (*UnsubscribeResult, error) {
	log := logging.WithComponent("unsubscribe")

	msg, err := a.messageStore.Get(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	if msg == nil {
		return nil, fmt.Errorf("message not found: %s", messageID)
	}

	info, err := a.detectUnsubscribe(msg)
	if err != nil {
		return nil, err
	}
	if !info.Available {
		return nil, fmt.Errorf("this message has no unsubscribe option")
	}

	// 1. RFC 8058 one-click POST (silent)
	if info.OneClick {
		target := unsubscribe.FirstHTTPS(info.HTTPURLs)
		perr := unsubscribe.PostOneClick(a.ctx, target, "Aerion/"+Version)
		if perr == nil {
			log.Info().Str("message_id", messageID).Str("method", "one-click").Msg("Unsubscribed")
			return &UnsubscribeResult{Method: unsubscribe.MethodOneClick, Target: unsubscribe.HostOf(target)}, nil
		}
		log.Warn().Err(perr).Str("message_id", messageID).Msg("One-click unsubscribe failed, trying fallback")
	}

	// 2. mailto: send the unsubscribe e-mail from the receiving identity
	if info.MailtoURL != "" {
		to, merr := a.sendUnsubscribeMail(msg, info.MailtoURL)
		if merr == nil {
			log.Info().Str("message_id", messageID).Str("method", "mailto").Msg("Unsubscribed")
			return &UnsubscribeResult{Method: unsubscribe.MethodMailto, Target: to}, nil
		}
		log.Warn().Err(merr).Str("message_id", messageID).Msg("Mailto unsubscribe failed, trying fallback")
		if len(info.HTTPURLs) == 0 {
			return nil, fmt.Errorf("failed to send unsubscribe e-mail: %w", merr)
		}
	}

	// 3. Open the unsubscribe page in the browser (header URL, else body link)
	pageURL := unsubscribe.FirstHTTPS(info.HTTPURLs)
	if pageURL == "" && len(info.HTTPURLs) > 0 {
		pageURL = info.HTTPURLs[0]
	}
	if pageURL == "" {
		pageURL = info.BodyURL
	}
	if pageURL == "" {
		return nil, fmt.Errorf("unsubscribe failed")
	}
	if err := a.OpenURL(pageURL); err != nil {
		return nil, fmt.Errorf("failed to open unsubscribe page: %w", err)
	}
	log.Info().Str("message_id", messageID).Str("method", "browser").Msg("Opened unsubscribe page")
	return &UnsubscribeResult{Method: unsubscribe.MethodBrowser, Target: unsubscribe.HostOf(pageURL)}, nil
}

// detectUnsubscribe loads (or backfills) the List-Unsubscribe headers for a
// message and runs detection.
func (a *App) detectUnsubscribe(msg *message.Message) (*unsubscribe.Info, error) {
	log := logging.WithComponent("unsubscribe")

	header, post, known, err := a.messageStore.GetListUnsubscribe(msg.ID)
	if err != nil {
		return nil, err
	}

	// Rows synced before migration 45 have never been inspected: fetch just
	// the header block from IMAP once and persist the result.
	if !known && msg.UID > 0 && a.syncEngine != nil {
		ctx, cancel := context.WithTimeout(a.ctx, headerBackfillTimeout)
		raw, ferr := a.syncEngine.FetchMessageHeaders(ctx, msg.AccountID, msg.FolderID, msg.UID)
		cancel()
		if ferr == nil {
			header, post = a.syncEngine.ExtractListUnsubscribe(raw)
			if serr := a.messageStore.SetListUnsubscribe(msg.ID, header, post); serr != nil {
				log.Debug().Err(serr).Str("message_id", msg.ID).Msg("Failed to persist list-unsubscribe headers")
			}
		} else {
			// Offline or message gone: fall back to body detection, retry next time
			log.Debug().Err(ferr).Str("message_id", msg.ID).Msg("Header backfill failed")
		}
	}

	return unsubscribe.Detect(header, post, msg.BodyHTML), nil
}

// sendUnsubscribeMail sends the e-mail described by a List-Unsubscribe
// mailto: URI from the identity that received the message. Returns the
// recipient address.
func (a *App) sendUnsubscribeMail(msg *message.Message, mailtoURL string) (string, error) {
	data := ParseMailtoURL(mailtoURL)
	if data == nil || len(data.To) == 0 {
		return "", fmt.Errorf("invalid mailto unsubscribe address")
	}

	identities, err := a.accountStore.GetIdentities(msg.AccountID)
	if err != nil {
		return "", fmt.Errorf("failed to get identities: %w", err)
	}
	var from smtp.Address
	if id := selectReplyFromIdentity(identities, msg); id != nil {
		from = smtp.Address{Name: id.Name, Address: id.Email}
	} else {
		acc, accErr := a.accountStore.Get(msg.AccountID)
		if accErr != nil || acc == nil {
			return "", fmt.Errorf("failed to get account: %v", accErr)
		}
		from = smtp.Address{Name: acc.Name, Address: acc.Email}
	}

	subject := data.Subject
	if subject == "" {
		subject = "Unsubscribe"
	}
	body := data.Body
	if body == "" {
		body = "Unsubscribe"
	}

	compose := smtp.ComposeMessage{
		From:     from,
		Subject:  subject,
		TextBody: body,
		// List-management robots are not people: keep them out of autocomplete
		SkipContacts: true,
	}
	for _, addr := range data.To {
		compose.To = append(compose.To, smtp.Address{Address: addr})
	}
	for _, addr := range data.Cc {
		compose.Cc = append(compose.Cc, smtp.Address{Address: addr})
	}

	if _, err := a.composeOps.sendMessage(a.ctx, msg.AccountID, compose, nil); err != nil {
		return "", err
	}
	return strings.Join(data.To, ", "), nil
}
