package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	imapPkg "github.com/hkdb/aerion/internal/imap"
	"github.com/hkdb/aerion/internal/message"
	"github.com/hkdb/aerion/internal/smime"
)

// ProcessedBody holds the parsed body content and attachments for a message
type ProcessedBody struct {
	MessageID      string
	BodyHTML       string
	BodyText       string
	Snippet        string
	HasAttachments bool
	Attachments    []*message.Attachment  // Extracted during parsing (no re-parse needed)
	RawBytes       []byte                 // For on-demand attachment content fetch
	SMIMEResult    *smime.SignatureResult  // S/MIME verification result
	SMIMERawBody   []byte                 // Raw S/MIME body for on-view processing
	SMIMEEncrypted bool                   // Whether the message is encrypted
	PGPRawBody     []byte                 // Raw PGP body for on-view processing
	PGPEncrypted   bool                   // Whether the message is PGP encrypted
}

// FetchMessageBody fetches the body for a single message on-demand.
// Uses streaming fetch internally to avoid blocking on .Collect().
func (e *Engine) FetchMessageBody(ctx context.Context, accountID, messageID string) (*message.Message, error) {
	// Get message from store to get UID and folder
	uid, folderID, err := e.messageStore.GetMessageUIDAndFolder(messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message info: %w", err)
	}

	// Get folder to get path
	f, err := e.folderStore.Get(folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	e.log.Debug().
		Str("messageID", messageID).
		Uint32("uid", uid).
		Str("folder", f.Path).
		Msg("Fetching message body on-demand")

	// Get a connection from the pool
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer e.pool.Release(conn)

	// Select the mailbox
	_, err = conn.Client().SelectMailbox(ctx, f.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to select mailbox: %w", err)
	}

	// Use fetchMessageBodiesBatch for streaming fetch (avoids .Collect() blocking)
	uidToMessageID := map[uint32]string{uid: messageID}
	results, err := e.fetchMessageBodiesBatch(ctx, conn.Client().RawClient(), uidToMessageID)
	if err != nil {
		return nil, fmt.Errorf("fetch body failed: %w", err)
	}

	result, ok := results[uid]
	if !ok || result == nil {
		// Message no longer exists on server — clean up the ghost
		e.log.Warn().Str("messageID", messageID).Uint32("uid", uid).Msg("Message not found on server, deleting ghost")
		if delErr := e.messageStore.Delete(messageID); delErr != nil {
			e.log.Debug().Err(delErr).Str("messageID", messageID).Msg("Failed to delete ghost message")
		}
		return nil, fmt.Errorf("message not found on server")
	}

	// Update message in store
	if err := e.messageStore.UpdateBody(messageID, result.BodyHTML, result.BodyText, result.Snippet, result.HasAttachments); err != nil {
		return nil, fmt.Errorf("failed to update message body: %w", err)
	}

	// Store attachments if present
	if result.HasAttachments && e.attachmentStore != nil {
		for _, att := range result.Attachments {
			if err := e.attachmentStore.Create(att); err != nil {
				e.log.Debug().Err(err).Str("filename", att.Filename).Msg("Failed to save attachment metadata")
			}
		}
	}

	// Return updated message
	return e.messageStore.Get(messageID)
}

// fetchMessageBodiesBatch fetches bodies for multiple messages in a single IMAP command
// The mailbox must already be selected by the caller.
// Returns a map of UID -> ProcessedBody for successfully fetched messages.
//
// Uses streaming (Next() loop) instead of Collect() to:
// - Avoid indefinite blocking if connection hangs
// - Allow context cancellation between messages
// - Return partial results if connection dies mid-batch
func (e *Engine) fetchMessageBodiesBatch(ctx context.Context, client *imapclient.Client, uidToMessageID map[uint32]string) (map[uint32]*ProcessedBody, error) {
	if len(uidToMessageID) == 0 {
		return make(map[uint32]*ProcessedBody), nil
	}

	// Check context
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Build UID set for batch fetch
	uidSet := imap.UIDSet{}
	for uid := range uidToMessageID {
		uidSet.AddNum(imap.UID(uid))
	}

	e.log.Debug().
		Int("count", len(uidToMessageID)).
		Msg("Fetching message bodies in batch")

	fetchOptions := &imap.FetchOptions{
		UID: true,
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierNone, // Full message
				Peek:      true,                   // Don't mark as read
			},
		},
		RFC822Size: true,
	}

	fetchCmd := client.Fetch(uidSet, fetchOptions)
	results := make(map[uint32]*ProcessedBody)

	// Stream messages one at a time instead of blocking on Collect()
	// This allows cancellation between messages and returns partial results on error
	for {
		// Check for cancellation between messages
		if ctx.Err() != nil {
			fetchCmd.Close()
			e.log.Warn().
				Int("fetched", len(results)).
				Int("requested", len(uidToMessageID)).
				Msg("Fetch cancelled, returning partial results")
			return results, ctx.Err()
		}

		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		// Extract UID and body section from streamed message
		var fetchedUID imap.UID
		var rawBytes []byte
		var gotBodySection bool

		for {
			item := msg.Next()
			if item == nil {
				break
			}

			switch data := item.(type) {
			case imapclient.FetchItemDataUID:
				fetchedUID = data.UID
			case imapclient.FetchItemDataBodySection:
				gotBodySection = true
				// Read body from literal reader with size limit to prevent memory exhaustion
				if data.Literal != nil {
					lr := io.LimitReader(data.Literal, maxMessageSize)
					var err error
					rawBytes, err = io.ReadAll(lr)
					if err != nil {
						e.log.Warn().
							Err(err).
							Uint32("uid", uint32(fetchedUID)).
							Msg("Failed to read body literal, continuing with partial data")
						// Keep whatever we got (may be partial)
					}
					// Log if we hit the size limit
					if int64(len(rawBytes)) == maxMessageSize {
						e.log.Warn().
							Uint32("uid", uint32(fetchedUID)).
							Int64("maxSize", maxMessageSize).
							Msg("Message body truncated at size limit")
					}
				} else {
					e.log.Warn().
						Uint32("uid", uint32(fetchedUID)).
						Msg("Body section has nil Literal reader")
				}
			}
		}

		// Log if we didn't receive a body section at all
		if !gotBodySection && fetchedUID != 0 {
			e.log.Warn().
				Uint32("uid", uint32(fetchedUID)).
				Msg("No body section in IMAP response for message")
		}

		uid := uint32(fetchedUID)
		if uid == 0 {
			e.log.Warn().Msg("Received message without UID in batch response")
			continue
		}

		messageID, ok := uidToMessageID[uid]
		if !ok {
			e.log.Warn().Uint32("uid", uid).Msg("Received unexpected UID in batch response")
			continue
		}

		if len(rawBytes) == 0 {
			e.log.Warn().Uint32("uid", uid).Str("messageID", messageID).Msg("Empty message body — deleting ghost message")
			if delErr := e.messageStore.Delete(messageID); delErr != nil {
				e.log.Warn().Err(delErr).Str("messageID", messageID).Msg("Failed to delete ghost message")
			}
			continue
		}

		e.log.Debug().
			Uint32("uid", uid).
			Int("bodySize", len(rawBytes)).
			Msg("Processing message body")

		// Parse body content with timeout, extracting attachments in the same pass
		parsed := e.parseMessageBodyFull(rawBytes, messageID, 30*time.Second)

		// Sanitize HTML
		bodyHTML := parsed.BodyHTML
		if bodyHTML != "" {
			bodyHTML = e.sanitizer.Sanitize(bodyHTML)
		}

		// Generate snippet
		var snippet string
		if parsed.BodyText != "" {
			snippet = generateSnippet(parsed.BodyText, 200)
		} else if bodyHTML != "" {
			snippet = generateSnippet(stripHTMLTags(bodyHTML), 200)
		}

		results[uid] = &ProcessedBody{
			MessageID:      messageID,
			BodyHTML:       bodyHTML,
			BodyText:       parsed.BodyText,
			Snippet:        snippet,
			HasAttachments: parsed.HasAttachments,
			Attachments:    parsed.Attachments,
			RawBytes:       rawBytes,
			SMIMEResult:    parsed.SMIMEResult,
			SMIMERawBody:   parsed.SMIMERawBody,
			SMIMEEncrypted: parsed.SMIMEEncrypted,
			PGPRawBody:     parsed.PGPRawBody,
			PGPEncrypted:   parsed.PGPEncrypted,
		}
	}

	if err := fetchCmd.Close(); err != nil {
		e.log.Warn().Err(err).
			Int("fetched", len(results)).
			Int("requested", len(uidToMessageID)).
			Msg("Fetch close error, returning partial results")
		// Return what we have, don't fail completely
		// Partial content is better than no content
	}

	e.log.Debug().
		Int("fetched", len(results)).
		Int("requested", len(uidToMessageID)).
		Msg("Batch fetch complete")

	return results, nil
}

// recordBodyFetchAttempts charges a persistent parse attempt to every requested
// message that did not come back with a usable body this round — either the
// server returned nothing for it, or it parsed to an empty body. Encrypted
// messages (which legitimately have an empty plaintext body until decrypted on
// view) count as resolved. Once a message reaches MaxBodyParseAttempts it is
// excluded from future body-fetch queries (see needsBodyPredicate), so a
// permanently-unparseable message is fetched at most MaxBodyParseAttempts times
// total instead of on every sync forever.
func (e *Engine) recordBodyFetchAttempts(requestedIDs []string, updates []message.BodyUpdate) {
	if len(requestedIDs) == 0 {
		return
	}

	resolved := make(map[string]bool, len(updates))
	for _, u := range updates {
		if u.BodyHTML != "" || u.BodyText != "" || u.SMIMEEncrypted || u.PGPEncrypted {
			resolved[u.MessageID] = true
		}
	}

	var unresolved []string
	for _, id := range requestedIDs {
		if !resolved[id] {
			unresolved = append(unresolved, id)
		}
	}
	if len(unresolved) == 0 {
		return
	}

	exhausted, err := e.messageStore.IncrementBodyAttempts(unresolved)
	if err != nil {
		e.log.Warn().Err(err).Int("count", len(unresolved)).Msg("Failed to record body fetch attempts")
		return
	}
	for _, id := range exhausted {
		e.log.Warn().
			Str("messageID", id).
			Int("maxAttempts", message.MaxBodyParseAttempts).
			Msg("Giving up on message body after max attempts — will not retry in future syncs")
	}
}

// FetchBodiesInBackground fetches bodies for messages that don't have them yet.
// This is called after headers sync to fetch bodies in the background.
// syncPeriodDays limits body fetching to messages within the sync period (0 = all messages).
//
// OPTIMIZED: Uses batch IMAP FETCH to fetch multiple message bodies in a single command,
// reducing network round-trips significantly. Uses hybrid byte+count batching (like Geary)
// for memory safety and efficiency:
//   - Max 512KB per batch (memory bounded)
//   - Max 50 messages per batch (even if small)
//   - Min 1 message per batch (handles oversized emails)
//
// Pipeline design for maximum throughput:
//  1. Wait for previous batch's goroutine (if any)
//  2. Apply DB updates from previous batch
//  3. Query candidates and build byte-aware batch
//  4. Fetch bodies via IMAP
//  5. Launch goroutine to parse/sanitize (DB update happens in step 2 of next iteration)
//  6. Repeat
//
// This allows IMAP fetch (network-bound) to run in parallel with parsing (CPU-bound).
// DB updates are synchronous relative to the next DB query to prevent race conditions.
//
// Uses a single IMAP connection for efficiency (reuses connection for all body fetches).
// Includes error recovery: on connection errors, discards dead connection and gets a new one.
// Returns error only if connection recovery fails - individual message failures are logged and skipped.
func (e *Engine) FetchBodiesInBackground(ctx context.Context, accountID, folderID string, syncPeriodDays int) error {
	// Check context at start
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Get folder to get path
	f, err := e.folderStore.Get(folderID)
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	// Calculate sync date cutoff
	var sinceDate time.Time
	if syncPeriodDays > 0 {
		sinceDate = time.Now().AddDate(0, 0, -syncPeriodDays)
	}

	e.log.Debug().
		Str("account", accountID).
		Str("folder", f.Path).
		Int("syncPeriodDays", syncPeriodDays).
		Msg("Fetching message bodies in background (hybrid batch mode)")

	// Get a SINGLE connection from the pool - reused for all body fetches
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	// Note: We manage connection lifecycle manually due to recovery logic
	// Don't use defer e.pool.Release(conn) - we handle it explicitly

	// Select the mailbox ONCE
	_, err = conn.Client().SelectMailbox(ctx, f.Path)
	if err != nil {
		e.pool.Release(conn)
		return fmt.Errorf("failed to select mailbox: %w", err)
	}

	// Get total count of messages without body (respecting sync period)
	totalWithoutBody, err := e.messageStore.CountMessagesWithoutBody(folderID, sinceDate)
	if err != nil {
		e.pool.Release(conn)
		return fmt.Errorf("failed to count messages without body: %w", err)
	}

	if totalWithoutBody == 0 {
		e.log.Debug().Msg("All messages have bodies, nothing to fetch")
		// Emit 1/1 so frontend shows 100% complete for bodies phase
		e.emitProgress(accountID, folderID, 1, 1, "bodies")
		e.pool.Release(conn)
		return nil
	}

	e.log.Info().Int("count", totalWithoutBody).Msg("Fetching message bodies (hybrid batch mode)")

	// Emit initial progress so frontend knows body fetch has started
	e.emitProgress(accountID, folderID, 0, totalWithoutBody, "bodies")

	// Tracking for error recovery and progress
	failedBatches := 0      // consecutive batch failures
	connectionFailures := 0 // total connection recovery attempts
	fetched := 0
	failed := 0

	// Processing result from goroutine - contains parsed data ready for DB.
	// requestedIDs is every message the batch asked the server for, so we can tell
	// which ones came back unusable (empty parse or not returned) and charge them a
	// persistent parse attempt — see recordBodyFetchAttempts.
	type processingResult struct {
		requestedIDs []string
		bodyUpdates  []message.BodyUpdate
		attachments  []*message.Attachment
		fetchedCount int
	}

	// Channel and pending state for pipelined processing
	var pendingResultChan chan processingResult

	// Start heartbeat logging for long operations - shows sync is alive during long fetches
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	defer cancelHeartbeat()

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.log.Info().
					Int("fetched", fetched).
					Int("total", totalWithoutBody).
					Int("failed", failed).
					Str("folder", f.Path).
					Msg("Body fetch in progress (heartbeat)")
			case <-heartbeatCtx.Done():
				return
			}
		}
	}()

	for {
		// Step 1: Wait for previous batch's goroutine (if any)
		// Step 2: Apply DB updates from previous batch
		if pendingResultChan != nil {
			e.log.Debug().Msg("Waiting for previous batch goroutine to complete")
			result := <-pendingResultChan
			e.log.Debug().
				Int("bodyUpdates", len(result.bodyUpdates)).
				Int("attachments", len(result.attachments)).
				Int("fetchedCount", result.fetchedCount).
				Msg("Received result from processing goroutine")

			// Apply database updates - MUST complete before querying next batch
			if len(result.bodyUpdates) > 0 {
				e.log.Debug().Int("count", len(result.bodyUpdates)).Msg("Applying batch DB update")
				if err := e.messageStore.UpdateBodiesBatch(result.bodyUpdates); err != nil {
					e.log.Warn().Err(err).Msg("Failed to batch update bodies")
					failed += result.fetchedCount
				} else {
					fetched += result.fetchedCount
					e.log.Debug().Int("fetched", fetched).Int("total", totalWithoutBody).Msg("DB update successful")
				}
			} else {
				e.log.Warn().Int("fetchedCount", result.fetchedCount).Msg("No body updates in result - bodies may be lost!")
			}
			if len(result.attachments) > 0 {
				if err := e.attachmentStore.CreateBatch(result.attachments); err != nil {
					e.log.Warn().Err(err).Msg("Failed to batch create attachments")
					// Attachments failed but bodies were saved, don't count as failed
				}
			}

			// Charge a persistent parse attempt to any requested message that came
			// back unusable, so it is not re-fetched forever across future syncs.
			e.recordBodyFetchAttempts(result.requestedIDs, result.bodyUpdates)

			// Emit progress after DB update completes
			e.log.Debug().Int("fetched", fetched).Int("total", totalWithoutBody).Msg("Emitting progress")
			e.emitProgress(accountID, folderID, fetched, totalWithoutBody, "bodies")
			pendingResultChan = nil
		}

		// Check context before starting new batch
		if ctx.Err() != nil {
			e.log.Debug().Msg("Body fetch cancelled")
			e.pool.Release(conn)
			return ctx.Err()
		}

		// Step 3: Query candidates and build byte-aware batch
		// Get more candidates than we'll use to allow for byte-based selection
		candidates, err := e.messageStore.GetMessagesWithoutBodyAndSize(folderID, bodyBatchQueryLimit, sinceDate)
		if err != nil {
			e.pool.Release(conn)
			return fmt.Errorf("failed to get messages without body: %w", err)
		}

		e.log.Debug().
			Int("candidates", len(candidates)).
			Int("fetched", fetched).
			Int("failed", failed).
			Msg("Queried candidates for next batch")

		if len(candidates) == 0 {
			e.log.Debug().Msg("No more candidates, body sync complete")
			break // All done
		}

		// Messages that have exhausted their parse-attempt budget are already
		// excluded by GetMessagesWithoutBodyAndSize (body_attempts < max), so the
		// candidate list needs no further in-memory filtering.
		filteredCandidates := candidates

		// Adaptive batch sizing: use smaller batches for large mailboxes
		// This provides faster recovery if one batch fails and more frequent progress updates
		batchMaxMessages := bodyBatchMaxMessages
		batchMaxBytes := int64(bodyBatchMaxBytes)

		if totalWithoutBody > 1000 {
			batchMaxMessages = 25
			batchMaxBytes = 256 * 1024 // 256KB
			// Log only once (when we first enter the large mailbox mode)
			if fetched == 0 && failed == 0 {
				e.log.Info().
					Int("totalMessages", totalWithoutBody).
					Int("batchMaxMessages", batchMaxMessages).
					Int64("batchMaxBytes", batchMaxBytes).
					Msg("Using smaller batches for large mailbox")
			}
		}

		// Build batch using hybrid byte + count limits
		var batchIDs []string
		var batchBytes int64

		for _, msg := range filteredCandidates {
			msgSize := int64(msg.Size)
			if msgSize <= 0 {
				msgSize = 10 * 1024 // Assume 10KB for messages with unknown size
			}

			// Check if adding this message would exceed limits
			wouldExceedBytes := batchBytes+msgSize > batchMaxBytes && len(batchIDs) >= bodyBatchMinMessages
			wouldExceedCount := len(batchIDs) >= batchMaxMessages

			if wouldExceedBytes || wouldExceedCount {
				break // Batch is full
			}

			batchIDs = append(batchIDs, msg.ID)
			batchBytes += msgSize
		}

		if len(batchIDs) == 0 {
			e.log.Warn().Msg("No messages selected for batch")
			break
		}

		e.log.Debug().
			Int("batchSize", len(batchIDs)).
			Int64("batchBytes", batchBytes).
			Msg("Processing batch")

		// Get UIDs for all messages in batch (single DB query)
		uidInfos, err := e.messageStore.GetMessageUIDsAndFolder(batchIDs)
		if err != nil {
			e.log.Warn().Err(err).Msg("Failed to get UIDs for batch, skipping")
			failedBatches++
			if failedBatches > maxMessageRetries {
				e.log.Error().Int("failedBatches", failedBatches).Msg("Too many consecutive batch failures")
				break
			}
			continue
		}

		// Build UID -> messageID map for batch fetch
		uidToMessageID := make(map[uint32]string)
		for msgID, info := range uidInfos {
			uidToMessageID[info.UID] = msgID
		}

		if len(uidToMessageID) == 0 {
			e.log.Warn().Int("requested", len(batchIDs)).Msg("No valid UIDs found for batch")
			continue
		}

		// Step 4: Fetch bodies via IMAP - single round-trip for all messages in batch
		bodies, fetchErr := e.fetchMessageBodiesBatch(ctx, conn.Client().RawClient(), uidToMessageID)
		if fetchErr != nil {
			// Check if this is a connection error
			if imapPkg.IsConnectionError(fetchErr) {
				connectionFailures++

				// Check if we've exhausted connection recovery attempts
				if connectionFailures > maxConnectionRetries {
					e.log.Error().
						Int("connectionFailures", connectionFailures).
						Msg("Body fetch aborted - connection recovery failed")
					e.pool.Discard(conn)
					return fmt.Errorf("connection recovery failed after %d attempts", connectionFailures)
				}

				e.log.Debug().
					Err(fetchErr).
					Int("attempt", connectionFailures).
					Msg("Connection error during batch fetch, attempting recovery")

				// Discard dead connection and get a new one
				e.pool.Discard(conn)

				conn, err = e.pool.GetConnection(ctx, accountID)
				if err != nil {
					return fmt.Errorf("failed to get new connection after error: %w", err)
				}

				// Re-select mailbox on new connection
				_, err = conn.Client().SelectMailbox(ctx, f.Path)
				if err != nil {
					e.pool.Release(conn)
					return fmt.Errorf("failed to select mailbox on new connection: %w", err)
				}

				e.log.Debug().Msg("Connection recovered successfully, retrying batch")
				continue // Retry same batch
			}

			// Non-connection error
			e.log.Warn().Err(fetchErr).Msg("Batch fetch failed with non-connection error")
			failedBatches++
			if failedBatches > maxMessageRetries {
				e.log.Error().Int("failedBatches", failedBatches).Msg("Too many consecutive batch failures")
				break
			}
			continue
		}

		// Reset failure counters on success
		failedBatches = 0

		// If we got no bodies back, charge a parse attempt to every requested
		// message so they are eventually given up on (persistently) instead of
		// being re-queried forever.
		if len(bodies) == 0 {
			e.log.Warn().Int("requested", len(uidToMessageID)).Msg("IMAP returned no bodies for batch")
			e.recordBodyFetchAttempts(batchIDs, nil)
			failed += len(uidToMessageID)
			continue
		}

		// Step 5: Launch goroutine to build body updates
		// DB update will happen in step 2 of the NEXT iteration
		// Attachments were already extracted during parsing - no re-parse needed!
		resultChan := make(chan processingResult, 1)
		currentBodies := bodies // capture for goroutine

		go func() {
			startTime := time.Now()
			var bodyUpdates []message.BodyUpdate
			var allAttachments []*message.Attachment

			for _, pb := range currentBodies {
				// Build body update
				bu := message.BodyUpdate{
					MessageID:      pb.MessageID,
					BodyHTML:       pb.BodyHTML,
					BodyText:       pb.BodyText,
					Snippet:        pb.Snippet,
					HasAttachments: pb.HasAttachments,
					SMIMERawBody:   pb.SMIMERawBody,
					SMIMEEncrypted: pb.SMIMEEncrypted,
					PGPRawBody:     pb.PGPRawBody,
					PGPEncrypted:   pb.PGPEncrypted,
				}
				// Don't cache S/MIME or PGP verification status — computed fresh on each view
				bodyUpdates = append(bodyUpdates, bu)

				// Use pre-extracted attachments (no re-parsing!)
				if len(pb.Attachments) > 0 {
					allAttachments = append(allAttachments, pb.Attachments...)
				}
			}

			e.log.Debug().
				Int("bodyUpdates", len(bodyUpdates)).
				Int("attachments", len(allAttachments)).
				Dur("elapsed", time.Since(startTime)).
				Msg("Built body updates and attachments for batch")

			resultChan <- processingResult{
				requestedIDs: batchIDs,
				bodyUpdates:  bodyUpdates,
				attachments:  allAttachments,
				fetchedCount: len(currentBodies),
			}
		}()

		// Mark that we have pending work - will be processed in step 1-2 of next iteration
		pendingResultChan = resultChan
	}

	// Handle final batch if there's pending work
	if pendingResultChan != nil {
		result := <-pendingResultChan

		if len(result.bodyUpdates) > 0 {
			if err := e.messageStore.UpdateBodiesBatch(result.bodyUpdates); err != nil {
				e.log.Warn().Err(err).Msg("Failed to batch update bodies (final)")
				failed += result.fetchedCount
			} else {
				fetched += result.fetchedCount
			}
		}
		if len(result.attachments) > 0 {
			if err := e.attachmentStore.CreateBatch(result.attachments); err != nil {
				e.log.Warn().Err(err).Msg("Failed to batch create attachments (final)")
			}
		}

		// Charge a persistent parse attempt to any unusable message in the final batch.
		e.recordBodyFetchAttempts(result.requestedIDs, result.bodyUpdates)

		e.emitProgress(accountID, folderID, fetched, totalWithoutBody, "bodies")
	}

	// Release connection when done
	e.pool.Release(conn)

	// Log summary
	if failed > 0 {
		e.log.Info().
			Int("fetched", fetched).
			Int("failed", failed).
			Int("total", totalWithoutBody).
			Msg("Body fetch complete with failures (hybrid batch mode)")
	} else {
		e.log.Info().
			Int("fetched", fetched).
			Int("total", totalWithoutBody).
			Msg("Body fetch complete (hybrid batch mode)")
	}

	return nil
}

// FetchRawMessage fetches the raw RFC822 content of a message from the IMAP server.
// Uses streaming fetch to avoid blocking on .Collect().
func (e *Engine) FetchRawMessage(ctx context.Context, accountID, folderID string, uid uint32) ([]byte, error) {
	// Get folder path
	f, err := e.folderStore.Get(folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}
	if f == nil {
		return nil, fmt.Errorf("folder not found: %s", folderID)
	}

	// Get a connection from the pool
	conn, err := e.pool.GetConnection(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection: %w", err)
	}
	defer e.pool.Release(conn)

	// Select the mailbox
	_, err = conn.Client().SelectMailbox(ctx, f.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to select mailbox: %w", err)
	}

	// Fetch the raw message
	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(uid))

	fetchOptions := &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{
			{
				Specifier: imap.PartSpecifierNone,
				Peek:      true,
			},
		},
	}

	fetchCmd := conn.Client().RawClient().Fetch(uidSet, fetchOptions)

	// Stream the single message instead of blocking on Collect()
	var rawBytes []byte

	msg := fetchCmd.Next()
	if msg == nil {
		fetchCmd.Close()
		return nil, fmt.Errorf("message not found: UID %d", uid)
	}

	// Extract body section from streamed message
	for {
		item := msg.Next()
		if item == nil {
			break
		}

		if data, ok := item.(imapclient.FetchItemDataBodySection); ok {
			if data.Literal != nil {
				lr := io.LimitReader(data.Literal, maxMessageSize)
				rawBytes, err = io.ReadAll(lr)
				if err != nil {
					fetchCmd.Close()
					return nil, fmt.Errorf("failed to read message body: %w", err)
				}
				break
			}
		}
	}

	fetchCmd.Close()

	if len(rawBytes) == 0 {
		return nil, fmt.Errorf("message body not found: UID %d", uid)
	}

	return rawBytes, nil
}

// buildMessageFromStreamedData constructs a Message from streamed IMAP data.
// Used by FetchServerMessage for server search results.
func (e *Engine) buildMessageFromStreamedData(accountID, folderID string, uid imap.UID, envelope *imap.Envelope, flags []imap.Flag, rfc822Size int64, rawBytes []byte) *message.Message {
	m := &message.Message{
		AccountID:  accountID,
		FolderID:   folderID,
		UID:        uint32(uid),
		ReceivedAt: time.Now().UTC(),
		Size:       int(rfc822Size),
	}

	// Parse envelope using shared helper
	applyEnvelopeToMessage(m, envelope)

	// Extract References and Disposition-Notification-To from raw message
	var references []string
	if len(rawBytes) > 0 {
		references = e.extractReferences(rawBytes)
		m.ReadReceiptTo = e.extractDispositionNotificationTo(rawBytes)
	}

	// Store references as JSON array
	if len(references) > 0 {
		refsJSON, _ := json.Marshal(references)
		m.References = string(refsJSON)
	}

	// Parse flags using shared helper
	applyFlagsToMessage(m, flags)

	// Parse message body
	if len(rawBytes) > 0 {
		bodyText, bodyHTML, hasAttachments := e.parseMessageBody(rawBytes)
		m.BodyText = bodyText
		m.HasAttachments = hasAttachments

		// Sanitize HTML
		if bodyHTML != "" {
			m.BodyHTML = e.sanitizer.Sanitize(bodyHTML)
		}

		// Generate snippet
		if bodyText != "" {
			m.Snippet = generateSnippet(bodyText, 200)
		} else if bodyHTML != "" {
			m.Snippet = generateSnippet(stripHTMLTags(bodyHTML), 200)
		}
	}

	return m
}

