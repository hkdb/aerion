package sync

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/hkdb/aerion/internal/message"
)

// IMAPSearchResponse wraps search results with the total count of matching UIDs.
// When a limit is applied, TotalCount may exceed len(Results).
type IMAPSearchResponse struct {
	Results    []*IMAPSearchResult `json:"results"`
	TotalCount int                 `json:"totalCount"`
}

// IMAPSearchResult represents a single IMAP server-side search result
type IMAPSearchResult struct {
	UID       uint32 `json:"uid"`
	MessageID string `json:"messageId,omitempty"` // Local DB ID if exists
	IsLocal   bool   `json:"isLocal"`             // Whether message exists in local DB

	// Envelope data (populated for all results)
	Subject   string    `json:"subject"`
	FromName  string    `json:"fromName"`
	FromEmail string    `json:"fromEmail"`
	Date      time.Time `json:"date"`
	Snippet   string    `json:"snippet,omitempty"` // Only for local messages

	// Flags
	IsRead         bool `json:"isRead"`
	IsStarred      bool `json:"isStarred"`
	HasAttachments bool `json:"hasAttachments"`

	// Context
	AccountID  string `json:"accountId"`
	FolderID   string `json:"folderId"`
	FolderName string `json:"folderName,omitempty"`
}

// buildSearchCriteria creates an IMAP search criteria that ORs across multiple fields.
// Produces: OR (FROM "q") (OR (SUBJECT "q") (OR (TO "q") (OR (CC "q") (BODY "q"))))
// This is more reliable than TEXT across IMAP implementations (Gmail in particular).
func buildSearchCriteria(query string) *imap.SearchCriteria {
	// Build nested OR: FROM | SUBJECT | TO | CC | BODY
	// go-imap Or field is [][2]SearchCriteria, each pair is OR(left, right)
	return &imap.SearchCriteria{
		Or: [][2]imap.SearchCriteria{
			{
				{Header: []imap.SearchCriteriaHeaderField{{Key: "FROM", Value: query}}},
				{Or: [][2]imap.SearchCriteria{
					{
						{Header: []imap.SearchCriteriaHeaderField{{Key: "SUBJECT", Value: query}}},
						{Or: [][2]imap.SearchCriteria{
							{
								{Header: []imap.SearchCriteriaHeaderField{{Key: "TO", Value: query}}},
								{Or: [][2]imap.SearchCriteria{
									{
										{Header: []imap.SearchCriteriaHeaderField{{Key: "CC", Value: query}}},
										{Body: []string{query}},
									},
								}},
							},
						}},
					},
				}},
			},
		},
	}
}

// IMAPSearch performs a server-side IMAP SEARCH query and returns results.
// For each matching UID, checks if the message exists locally and enriches with local data.
// Non-local messages get envelope data fetched from the server.
// When limit > 0, only the newest `limit` UIDs are processed but TotalCount reflects all matches.
func (e *Engine) IMAPSearch(ctx context.Context, accountID, folderID, query string, limit int) (*IMAPSearchResponse, error) {
	// Get folder path
	f, err := e.folderStore.Get(folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}
	if f == nil {
		return nil, fmt.Errorf("folder not found: %s", folderID)
	}

	// Acquire connection
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer func() { e.pool.Release(conn) }()

	// Select mailbox
	_, err = conn.Client().SelectMailbox(ctx, f.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to select mailbox: %w", err)
	}

	// IMAP SEARCH using OR across multiple fields for maximum compatibility.
	// Many servers (notably Gmail) have limited TEXT/BODY search implementations,
	// so we explicitly OR across FROM, SUBJECT, TO, CC, and BODY fields.
	// This produces: UID SEARCH OR FROM "q" OR SUBJECT "q" OR TO "q" OR CC "q" BODY "q"
	client := conn.Client().RawClient()
	criteria := buildSearchCriteria(query)
	searchCmd := client.UIDSearch(criteria, nil)

	// Run Wait() in a goroutine to allow context cancellation
	type searchResult struct {
		data *imap.SearchData
		err  error
	}
	resultCh := make(chan searchResult, 1)
	go func() {
		data, err := searchCmd.Wait()
		resultCh <- searchResult{data, err}
	}()

	var uids []uint32
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return nil, fmt.Errorf("IMAP search failed: %w", result.err)
		}
		for _, uid := range result.data.AllUIDs() {
			uids = append(uids, uint32(uid))
		}
	}

	if len(uids) == 0 {
		return &IMAPSearchResponse{TotalCount: 0}, nil
	}

	totalCount := len(uids)

	// When limit is set, keep only the newest UIDs (highest UID = newest).
	// Sort descending first so we take the most recent ones.
	if limit > 0 && len(uids) > limit {
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		uids = uids[:limit]
	}

	// Check which UIDs exist locally and collect non-local ones
	var results []*IMAPSearchResult
	var nonLocalUIDs []uint32

	for _, uid := range uids {
		localMsg, err := e.messageStore.GetByUID(folderID, uid)
		if err != nil {
			e.log.Warn().Err(err).Uint32("uid", uid).Msg("Failed to check local message")
			nonLocalUIDs = append(nonLocalUIDs, uid)
			continue
		}
		if localMsg != nil {
			results = append(results, &IMAPSearchResult{
				UID:            uid,
				MessageID:      localMsg.ID,
				IsLocal:        true,
				Subject:        localMsg.Subject,
				FromName:       localMsg.FromName,
				FromEmail:      localMsg.FromEmail,
				Date:           localMsg.Date,
				Snippet:        localMsg.Snippet,
				IsRead:         localMsg.IsRead,
				IsStarred:      localMsg.IsStarred,
				HasAttachments: localMsg.HasAttachments,
				AccountID:      accountID,
				FolderID:       folderID,
				FolderName:     f.Name,
			})
			continue
		}
		nonLocalUIDs = append(nonLocalUIDs, uid)
	}

	// Batch-fetch envelopes for non-local UIDs
	if len(nonLocalUIDs) > 0 {
		envelopeResults, err := e.fetchEnvelopesForSearch(ctx, client, accountID, folderID, f.Name, nonLocalUIDs)
		if err != nil {
			e.log.Warn().Err(err).Int("count", len(nonLocalUIDs)).Msg("Failed to fetch envelopes for non-local search results")
		} else {
			results = append(results, envelopeResults...)
		}
	}

	// Sort by date descending (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Date.After(results[j].Date)
	})

	return &IMAPSearchResponse{
		Results:    results,
		TotalCount: totalCount,
	}, nil
}

// fetchEnvelopesForSearch fetches envelope data for non-local UIDs found by IMAP SEARCH.
// Processes in batches of 50 to avoid overwhelming the server.
func (e *Engine) fetchEnvelopesForSearch(ctx context.Context, client *imapclient.Client, accountID, folderID, folderName string, uids []uint32) ([]*IMAPSearchResult, error) {
	var results []*IMAPSearchResult

	for i := 0; i < len(uids); i += headerBatchSize {
		end := i + headerBatchSize
		if end > len(uids) {
			end = len(uids)
		}
		batch := uids[i:end]

		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		uidSet := imap.UIDSet{}
		for _, uid := range batch {
			uidSet.AddNum(imap.UID(uid))
		}

		fetchOptions := &imap.FetchOptions{
			Envelope: true,
			Flags:    true,
			UID:      true,
		}

		fetchCmd := client.Fetch(uidSet, fetchOptions)

		for {
			if ctx.Err() != nil {
				fetchCmd.Close()
				return results, ctx.Err()
			}

			msg := fetchCmd.Next()
			if msg == nil {
				break
			}

			var fetchedUID imap.UID
			var envelope *imap.Envelope
			var flags []imap.Flag

			for {
				item := msg.Next()
				if item == nil {
					break
				}
				switch data := item.(type) {
				case imapclient.FetchItemDataUID:
					fetchedUID = data.UID
				case imapclient.FetchItemDataEnvelope:
					envelope = data.Envelope
				case imapclient.FetchItemDataFlags:
					flags = data.Flags
				}
			}

			if fetchedUID == 0 {
				continue
			}

			r := &IMAPSearchResult{
				UID:        uint32(fetchedUID),
				IsLocal:    false,
				AccountID:  accountID,
				FolderID:   folderID,
				FolderName: folderName,
			}

			if envelope != nil {
				r.Subject = envelope.Subject
				r.Date = envelope.Date.UTC()
				if len(envelope.From) > 0 {
					r.FromName = envelope.From[0].Name
					r.FromEmail = envelope.From[0].Addr()
				}
			}

			for _, flag := range flags {
				switch flag {
				case imap.FlagSeen:
					r.IsRead = true
				case imap.FlagFlagged:
					r.IsStarred = true
				}
			}

			results = append(results, r)
		}

		if err := fetchCmd.Close(); err != nil {
			e.log.Warn().Err(err).Msg("Envelope fetch close error in search")
		}
	}

	return results, nil
}

// FetchServerMessage fetches a full message by UID from IMAP, parses it, saves to local DB,
// and returns it. Used when a user interacts with a non-local server search result.
func (e *Engine) FetchServerMessage(ctx context.Context, accountID, folderID string, uid uint32) (*message.Message, error) {
	// Get folder path
	f, err := e.folderStore.Get(folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}
	if f == nil {
		return nil, fmt.Errorf("folder not found: %s", folderID)
	}

	// Check if already exists locally
	existing, err := e.messageStore.GetByUID(folderID, uid)
	if err != nil {
		return nil, fmt.Errorf("failed to check local message: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Acquire connection
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer func() { e.pool.Release(conn) }()

	// Select mailbox
	_, err = conn.Client().SelectMailbox(ctx, f.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to select mailbox: %w", err)
	}

	// Fetch full message
	client := conn.Client().RawClient()
	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(uid))

	fetchOptions := &imap.FetchOptions{
		Envelope:   true,
		Flags:      true,
		RFC822Size: true,
		UID:        true,
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierNone,
				Peek:      true,
			},
		},
	}

	fetchCmd := client.Fetch(uidSet, fetchOptions)

	msg := fetchCmd.Next()
	if msg == nil {
		fetchCmd.Close()
		return nil, fmt.Errorf("message not found on server: UID %d", uid)
	}

	var fetchedUID imap.UID
	var envelope *imap.Envelope
	var flags []imap.Flag
	var rfc822Size int64
	var rawBytes []byte

	for {
		item := msg.Next()
		if item == nil {
			break
		}
		switch data := item.(type) {
		case imapclient.FetchItemDataUID:
			fetchedUID = data.UID
		case imapclient.FetchItemDataEnvelope:
			envelope = data.Envelope
		case imapclient.FetchItemDataFlags:
			flags = data.Flags
		case imapclient.FetchItemDataRFC822Size:
			rfc822Size = data.Size
		case imapclient.FetchItemDataBodySection:
			if data.Literal != nil {
				lr := io.LimitReader(data.Literal, maxMessageSize)
				rawBytes, err = io.ReadAll(lr)
				if err != nil {
					e.log.Warn().Err(err).Uint32("uid", uint32(fetchedUID)).Msg("Failed to read body literal")
				}
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		e.log.Warn().Err(err).Msg("Fetch close error for server message")
	}

	if fetchedUID == 0 {
		return nil, fmt.Errorf("received message without UID")
	}

	// Build and save message
	m := e.buildMessageFromStreamedData(accountID, folderID, fetchedUID, envelope, flags, rfc822Size, rawBytes)
	m.BodyFetched = true

	if err := e.messageStore.Create(m); err != nil {
		return nil, fmt.Errorf("failed to save message: %w", err)
	}

	// Extract and store attachments
	if m.HasAttachments && len(rawBytes) > 0 && e.attachmentStore != nil {
		attachments, err := e.attachExtractor.ExtractAttachments(m.ID, rawBytes)
		if err != nil {
			e.log.Debug().Err(err).Str("messageId", m.ID).Msg("Failed to extract attachments")
		} else {
			for _, att := range attachments {
				if att.Attachment.IsInline && len(att.Content) > 0 {
					att.Attachment.Content = att.Content
				}
				if err := e.attachmentStore.Create(att.Attachment); err != nil {
					e.log.Debug().Err(err).Str("filename", att.Attachment.Filename).Msg("Failed to save attachment metadata")
				}
			}
		}
	}

	// Compute and update thread ID
	threadID := e.computeThreadID(accountID, m)
	if threadID != "" && threadID != m.ThreadID {
		m.ThreadID = threadID
		if err := e.messageStore.UpdateThreadID(m.ID, threadID); err != nil {
			e.log.Warn().Err(err).Str("messageId", m.ID).Msg("Failed to update thread ID")
		}
	}

	// Reconcile threads
	if err := e.messageStore.ReconcileThreadsForNewMessage(accountID, m.ID, m.MessageID, m.ThreadID, m.InReplyTo); err != nil {
		e.log.Warn().Err(err).Str("messageId", m.ID).Msg("Failed to reconcile threads")
	}

	// Reload from DB to pick up any thread_id changes from reconciliation.
	// ReconcileThreadsForNewMessage may update thread_id in the DB without
	// updating the in-memory m, causing the frontend to use a stale threadId.
	saved, reloadErr := e.messageStore.Get(m.ID)
	if reloadErr == nil && saved != nil {
		return saved, nil
	}
	return m, nil
}
