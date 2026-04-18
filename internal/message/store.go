package message

import (
	"database/sql"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/database"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// Store provides message persistence operations
type Store struct {
	db  *database.DB
	log zerolog.Logger
}

// NewStore creates a new message store
func NewStore(db *database.DB) *Store {
	return &Store{
		db:  db,
		log: logging.WithComponent("message-store"),
	}
}

// filterHavingClause returns a HAVING clause for conversation-level filtering.
// prefix should be "" for single-table queries or "m." for joined queries.
func filterHavingClause(filter, prefix string) string {
	switch filter {
	case "unread":
		return fmt.Sprintf(" HAVING SUM(CASE WHEN %sis_read = 0 THEN 1 ELSE 0 END) > 0", prefix)
	case "starred":
		return fmt.Sprintf(" HAVING MAX(CASE WHEN %sis_starred = 1 THEN 1 ELSE 0 END) = 1", prefix)
	case "attachments":
		return fmt.Sprintf(" HAVING MAX(CASE WHEN %shas_attachments = 1 THEN 1 ELSE 0 END) = 1", prefix)
	default:
		return ""
	}
}

// filterWhereClause returns a WHERE condition for count queries.
// prefix should be "" for single-table queries or "m." for joined queries.
func filterWhereClause(filter, prefix string) string {
	switch filter {
	case "unread":
		return fmt.Sprintf(" AND %sis_read = 0", prefix)
	case "starred":
		return fmt.Sprintf(" AND %sis_starred = 1", prefix)
	case "attachments":
		return fmt.Sprintf(" AND %shas_attachments = 1", prefix)
	default:
		return ""
	}
}

// ListByFolder returns message headers for a folder with pagination
func (s *Store) ListByFolder(folderID string, offset, limit int) ([]*MessageHeader, error) {
	query := `
		SELECT id, account_id, folder_id, uid, subject, from_name, from_email,
		       date, snippet, is_read, is_starred, has_attachments
		FROM messages
		WHERE folder_id = ?
		ORDER BY date DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.Query(query, folderID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var messages []*MessageHeader
	for rows.Next() {
		m := &MessageHeader{}
		var dateStr sql.NullString
		var snippet sql.NullString

		err := rows.Scan(
			&m.ID, &m.AccountID, &m.FolderID, &m.UID,
			&m.Subject, &m.FromName, &m.FromEmail,
			&dateStr, &snippet,
			&m.IsRead, &m.IsStarred, &m.HasAttachments,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		if dateStr.Valid && dateStr.String != "" {
			m.Date = parseTimeString(dateStr.String)
		}
		if snippet.Valid {
			m.Snippet = snippet.String
		}

		messages = append(messages, m)
	}

	return messages, nil
}

// ListConversationsUnifiedInbox returns conversations from all inbox folders across all accounts
// This is used for the unified inbox view
func (s *Store) ListConversationsUnifiedInbox(offset, limit int, sortOrder, filter string) ([]*Conversation, error) {
	// Determine sort direction
	orderClause := "ORDER BY latest_date DESC"
	if sortOrder == "oldest" {
		orderClause = "ORDER BY latest_date ASC"
	}

	// Query conversations from all inbox folders, joining with accounts for name and color
	query := `
		SELECT 
			COALESCE(m.thread_id, m.id) as conv_thread_id,
			MIN(m.subject) as subject,
			MAX(m.snippet) as snippet,
			COUNT(*) as message_count,
			SUM(CASE WHEN m.is_read = 0 THEN 1 ELSE 0 END) as unread_count,
			MAX(CASE WHEN m.has_attachments = 1 THEN 1 ELSE 0 END) as has_attachments,
			MAX(CASE WHEN m.is_starred = 1 THEN 1 ELSE 0 END) as is_starred,
			MAX(m.date) as latest_date,
			GROUP_CONCAT(m.id) as message_ids,
			MAX(CASE WHEN m.smime_encrypted = 1 OR m.pgp_encrypted = 1 THEN 1 ELSE 0 END) as is_encrypted,
			a.id as account_id,
			a.name as account_name,
			a.color as account_color,
			f.id as folder_id
		FROM messages m
		INNER JOIN folders f ON m.folder_id = f.id AND f.folder_type = 'inbox'
		INNER JOIN accounts a ON f.account_id = a.id AND a.enabled = 1
		GROUP BY COALESCE(m.thread_id, m.id), a.id` +
		filterHavingClause(filter, "m.") + `
		` + orderClause + `
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query unified inbox conversations: %w", err)
	}
	defer rows.Close()

	var conversations []*Conversation
	for rows.Next() {
		c := &Conversation{}
		var latestDateStr sql.NullString
		var snippet sql.NullString
		var messageIDsStr sql.NullString

		err := rows.Scan(
			&c.ThreadID,
			&c.Subject,
			&snippet,
			&c.MessageCount,
			&c.UnreadCount,
			&c.HasAttachments,
			&c.IsStarred,
			&latestDateStr,
			&messageIDsStr,
			&c.IsEncrypted,
			&c.AccountID,
			&c.AccountName,
			&c.AccountColor,
			&c.FolderID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan unified inbox conversation: %w", err)
		}

		if snippet.Valid {
			c.Snippet = snippet.String
		}
		if latestDateStr.Valid && latestDateStr.String != "" {
			c.LatestDate = parseTimeString(latestDateStr.String)
		}

		// Parse message IDs from comma-separated string
		if messageIDsStr.Valid && messageIDsStr.String != "" {
			c.MessageIDs = strings.Split(messageIDsStr.String, ",")
		}

		// Get participants for this conversation
		participants, err := s.getConversationParticipantsUnified(c.ThreadID, c.AccountID)
		if err != nil {
			s.log.Warn().Err(err).Str("threadId", c.ThreadID).Msg("Failed to get participants for unified inbox")
		}
		c.Participants = participants

		conversations = append(conversations, c)
	}

	return conversations, nil
}

// getConversationParticipantsUnified returns unique participants in a conversation across all inbox folders for an account
func (s *Store) getConversationParticipantsUnified(threadID, accountID string) ([]Address, error) {
	query := `
		SELECT DISTINCT m.from_name, m.from_email
		FROM messages m
		INNER JOIN folders f ON m.folder_id = f.id AND f.folder_type = 'inbox'
		WHERE f.account_id = ? AND COALESCE(m.thread_id, m.id) = ?
		ORDER BY m.date ASC
	`

	rows, err := s.db.Query(query, accountID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []Address
	seen := make(map[string]bool)

	for rows.Next() {
		var name, email string
		if err := rows.Scan(&name, &email); err != nil {
			continue
		}
		if !seen[email] {
			seen[email] = true
			participants = append(participants, Address{Name: name, Email: email})
		}
	}

	return participants, nil
}

// CountConversationsUnifiedInbox returns the total count of conversations across all inbox folders
func (s *Store) CountConversationsUnifiedInbox(filter string) (int, error) {
	filterCond := filterWhereClause(filter, "m.")
	wherePart := ""
	if filterCond != "" {
		wherePart = " WHERE" + filterCond[len(" AND"):]
	}

	query := `
		SELECT COUNT(DISTINCT COALESCE(m.thread_id, m.id) || '-' || a.id)
		FROM messages m
		INNER JOIN folders f ON m.folder_id = f.id AND f.folder_type = 'inbox'
		INNER JOIN accounts a ON f.account_id = a.id AND a.enabled = 1
	` + wherePart

	var count int
	err := s.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count unified inbox conversations: %w", err)
	}
	return count, nil
}

// GetUnifiedInboxUnreadCount returns the total unread message count across all inbox folders
// Uses the cached folder.unread_count values to stay consistent with sidebar folder counts
func (s *Store) GetUnifiedInboxUnreadCount() (int, error) {
	// First, log individual inbox folders for debugging
	debugQuery := `
		SELECT f.id, f.name, f.folder_type, f.unread_count, a.name as account_name, a.enabled
		FROM folders f
		INNER JOIN accounts a ON f.account_id = a.id
		WHERE f.folder_type = 'inbox'
	`
	rows, err := s.db.Query(debugQuery)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var folderID, folderName, folderType, accountName string
			var unreadCount int
			var enabled bool
			if err := rows.Scan(&folderID, &folderName, &folderType, &unreadCount, &accountName, &enabled); err == nil {
				s.log.Debug().
					Str("folderID", folderID).
					Str("folderName", folderName).
					Str("folderType", folderType).
					Int("unreadCount", unreadCount).
					Str("accountName", accountName).
					Bool("enabled", enabled).
					Msg("Inbox folder for unified count")
			}
		}
	}

	query := `
		SELECT COALESCE(SUM(f.unread_count), 0)
		FROM folders f
		INNER JOIN accounts a ON f.account_id = a.id AND a.enabled = 1
		WHERE f.folder_type = 'inbox'
	`

	var count int
	err = s.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count unified inbox unread: %w", err)
	}

	s.log.Debug().Int("unreadCount", count).Msg("GetUnifiedInboxUnreadCount (sum of folder counts)")
	return count, nil
}

// CountUnreadByFolder returns the unread message count for a folder
func (s *Store) CountUnreadByFolder(folderID string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE folder_id = ? AND is_read = 0", folderID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count unread messages: %w", err)
	}
	return count, nil
}

// GetUnreadMessageIDsByFolder returns the IDs of all unread messages in a folder
func (s *Store) GetUnreadMessageIDsByFolder(folderID string) ([]string, error) {
	rows, err := s.db.Query("SELECT id FROM messages WHERE folder_id = ? AND is_read = 0", folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query unread messages: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan message id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetReadMessageIDsByFolder returns the IDs of all read messages in a folder
func (s *Store) GetReadMessageIDsByFolder(folderID string) ([]string, error) {
	rows, err := s.db.Query("SELECT id FROM messages WHERE folder_id = ? AND is_read = 1", folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query read messages: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan message id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetAllIDsByFolder returns the IDs of all messages in a folder
func (s *Store) GetAllIDsByFolder(folderID string) ([]string, error) {
	rows, err := s.db.Query("SELECT id FROM messages WHERE folder_id = ?", folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan message id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Get returns a full message by ID
func (s *Store) Get(id string) (*Message, error) {
	query := `
		SELECT id, account_id, folder_id, uid, message_id, in_reply_to, thread_id,
		       subject, from_name, from_email, to_list, cc_list, bcc_list, reply_to, date,
		       snippet, is_read, is_starred, is_answered, is_forwarded, is_draft, is_deleted,
		       size, has_attachments, body_text, body_html, body_fetched,
		       read_receipt_to, read_receipt_handled,
		       smime_status, smime_signer_email, smime_signer_subject,
		       smime_encrypted, (smime_raw_body IS NOT NULL) as has_smime,
		       pgp_status, pgp_signer_email, pgp_signer_key_id,
		       pgp_encrypted, (pgp_raw_body IS NOT NULL) as has_pgp,
		       received_at
		FROM messages
		WHERE id = ?
	`

	m := &Message{}
	var messageID, inReplyTo, threadID, toList, ccList, bccList, replyTo, snippet, bodyText, bodyHTML, readReceiptTo sql.NullString
	var smimeStatus, smimeSignerEmail, smimeSignerSubject sql.NullString
	var pgpStatus, pgpSignerEmail, pgpSignerKeyID sql.NullString
	var dateStr, receivedAtStr sql.NullString

	err := s.db.QueryRow(query, id).Scan(
		&m.ID, &m.AccountID, &m.FolderID, &m.UID, &messageID, &inReplyTo, &threadID,
		&m.Subject, &m.FromName, &m.FromEmail, &toList, &ccList, &bccList, &replyTo, &dateStr,
		&snippet, &m.IsRead, &m.IsStarred, &m.IsAnswered, &m.IsForwarded, &m.IsDraft, &m.IsDeleted,
		&m.Size, &m.HasAttachments, &bodyText, &bodyHTML, &m.BodyFetched,
		&readReceiptTo, &m.ReadReceiptHandled,
		&smimeStatus, &smimeSignerEmail, &smimeSignerSubject,
		&m.SMIMEEncrypted, &m.HasSMIME,
		&pgpStatus, &pgpSignerEmail, &pgpSignerKeyID,
		&m.PGPEncrypted, &m.HasPGP,
		&receivedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	if messageID.Valid {
		m.MessageID = messageID.String
	}
	if inReplyTo.Valid {
		m.InReplyTo = inReplyTo.String
	}
	if threadID.Valid {
		m.ThreadID = threadID.String
	}
	if toList.Valid {
		m.ToList = toList.String
	}
	if ccList.Valid {
		m.CcList = ccList.String
	}
	if bccList.Valid {
		m.BccList = bccList.String
	}
	if replyTo.Valid {
		m.ReplyTo = replyTo.String
	}
	if dateStr.Valid && dateStr.String != "" {
		m.Date = parseTimeString(dateStr.String)
	}
	if snippet.Valid {
		m.Snippet = snippet.String
	}
	if bodyText.Valid {
		m.BodyText = bodyText.String
	}
	if bodyHTML.Valid {
		m.BodyHTML = bodyHTML.String
	}
	if readReceiptTo.Valid {
		m.ReadReceiptTo = readReceiptTo.String
	}
	if smimeStatus.Valid {
		m.SMIMEStatus = smimeStatus.String
	}
	if smimeSignerEmail.Valid {
		m.SMIMESignerEmail = smimeSignerEmail.String
	}
	if smimeSignerSubject.Valid {
		m.SMIMESignerSubject = smimeSignerSubject.String
	}
	if pgpStatus.Valid {
		m.PGPStatus = pgpStatus.String
	}
	if pgpSignerEmail.Valid {
		m.PGPSignerEmail = pgpSignerEmail.String
	}
	if pgpSignerKeyID.Valid {
		m.PGPSignerKeyID = pgpSignerKeyID.String
	}
	if receivedAtStr.Valid && receivedAtStr.String != "" {
		m.ReceivedAt = parseTimeString(receivedAtStr.String)
	}

	return m, nil
}

// GetByUID returns a message by folder ID and UID
func (s *Store) GetByUID(folderID string, uid uint32) (*Message, error) {
	query := `
		SELECT id, account_id, folder_id, uid, message_id, in_reply_to, thread_id,
		       subject, from_name, from_email, to_list, cc_list, bcc_list, reply_to, date,
		       snippet, is_read, is_starred, is_answered, is_forwarded, is_draft, is_deleted,
		       size, has_attachments, body_text, body_html, body_fetched,
		       read_receipt_to, read_receipt_handled,
		       smime_status, smime_signer_email, smime_signer_subject,
		       smime_encrypted, (smime_raw_body IS NOT NULL) as has_smime,
		       pgp_status, pgp_signer_email, pgp_signer_key_id,
		       pgp_encrypted, (pgp_raw_body IS NOT NULL) as has_pgp,
		       received_at
		FROM messages
		WHERE folder_id = ? AND uid = ?
	`

	m := &Message{}
	var messageID, inReplyTo, threadID, toList, ccList, bccList, replyTo, snippet, bodyText, bodyHTML, readReceiptTo sql.NullString
	var smimeStatus, smimeSignerEmail, smimeSignerSubject sql.NullString
	var pgpStatus, pgpSignerEmail, pgpSignerKeyID sql.NullString
	var dateStr, receivedAtStr sql.NullString

	err := s.db.QueryRow(query, folderID, uid).Scan(
		&m.ID, &m.AccountID, &m.FolderID, &m.UID, &messageID, &inReplyTo, &threadID,
		&m.Subject, &m.FromName, &m.FromEmail, &toList, &ccList, &bccList, &replyTo, &dateStr,
		&snippet, &m.IsRead, &m.IsStarred, &m.IsAnswered, &m.IsForwarded, &m.IsDraft, &m.IsDeleted,
		&m.Size, &m.HasAttachments, &bodyText, &bodyHTML, &m.BodyFetched,
		&readReceiptTo, &m.ReadReceiptHandled,
		&smimeStatus, &smimeSignerEmail, &smimeSignerSubject,
		&m.SMIMEEncrypted, &m.HasSMIME,
		&pgpStatus, &pgpSignerEmail, &pgpSignerKeyID,
		&m.PGPEncrypted, &m.HasPGP,
		&receivedAtStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	// Populate optional fields
	if messageID.Valid {
		m.MessageID = messageID.String
	}
	if inReplyTo.Valid {
		m.InReplyTo = inReplyTo.String
	}
	if threadID.Valid {
		m.ThreadID = threadID.String
	}
	if toList.Valid {
		m.ToList = toList.String
	}
	if ccList.Valid {
		m.CcList = ccList.String
	}
	if bccList.Valid {
		m.BccList = bccList.String
	}
	if replyTo.Valid {
		m.ReplyTo = replyTo.String
	}
	if dateStr.Valid && dateStr.String != "" {
		m.Date = parseTimeString(dateStr.String)
	}
	if snippet.Valid {
		m.Snippet = snippet.String
	}
	if bodyText.Valid {
		m.BodyText = bodyText.String
	}
	if bodyHTML.Valid {
		m.BodyHTML = bodyHTML.String
	}
	if readReceiptTo.Valid {
		m.ReadReceiptTo = readReceiptTo.String
	}
	if smimeStatus.Valid {
		m.SMIMEStatus = smimeStatus.String
	}
	if smimeSignerEmail.Valid {
		m.SMIMESignerEmail = smimeSignerEmail.String
	}
	if smimeSignerSubject.Valid {
		m.SMIMESignerSubject = smimeSignerSubject.String
	}
	if pgpStatus.Valid {
		m.PGPStatus = pgpStatus.String
	}
	if pgpSignerEmail.Valid {
		m.PGPSignerEmail = pgpSignerEmail.String
	}
	if pgpSignerKeyID.Valid {
		m.PGPSignerKeyID = pgpSignerKeyID.String
	}
	if receivedAtStr.Valid && receivedAtStr.String != "" {
		m.ReceivedAt = parseTimeString(receivedAtStr.String)
	}

	return m, nil
}

// Create creates a new message
func (s *Store) Create(m *Message) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.ReceivedAt.IsZero() {
		m.ReceivedAt = time.Now().UTC()
	}

	s.log.Debug().
		Str("id", m.ID).
		Str("subject", m.Subject).
		Str("messageID", m.MessageID).
		Str("threadID", m.ThreadID).
		Int("bodyTextLen", len(m.BodyText)).
		Int("bodyHTMLLen", len(m.BodyHTML)).
		Uint32("uid", m.UID).
		Msg("Creating message in store")

	query := `
		INSERT INTO messages (
			id, account_id, folder_id, uid, message_id, in_reply_to, references_list, thread_id,
			subject, from_name, from_email, to_list, cc_list, bcc_list, reply_to, date,
			snippet, is_read, is_starred, is_answered, is_forwarded, is_draft, is_deleted,
			size, has_attachments, body_text, body_html, body_fetched,
			read_receipt_to, read_receipt_handled, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query,
		m.ID, m.AccountID, m.FolderID, m.UID,
		nullString(m.MessageID), nullString(m.InReplyTo), nullString(m.References), nullString(m.ThreadID),
		m.Subject, m.FromName, m.FromEmail,
		nullString(m.ToList), nullString(m.CcList), nullString(m.BccList), nullString(m.ReplyTo),
		m.Date, nullString(m.Snippet),
		m.IsRead, m.IsStarred, m.IsAnswered, m.IsForwarded, m.IsDraft, m.IsDeleted,
		m.Size, m.HasAttachments,
		nullString(m.BodyText), nullString(m.BodyHTML), m.BodyFetched,
		nullString(m.ReadReceiptTo), m.ReadReceiptHandled,
		m.ReceivedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	return nil
}

// Upsert inserts a message or updates it if a row with the same (folder_id, uid) already exists.
// This handles cases where a previous copy was deleted but the stale row remains, or where
// the IMAP server reuses UIDs after EXPUNGE.
func (s *Store) Upsert(m *Message) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.ReceivedAt.IsZero() {
		m.ReceivedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO messages (
			id, account_id, folder_id, uid, message_id, in_reply_to, references_list, thread_id,
			subject, from_name, from_email, to_list, cc_list, bcc_list, reply_to, date,
			snippet, is_read, is_starred, is_answered, is_forwarded, is_draft, is_deleted,
			size, has_attachments, body_text, body_html, body_fetched,
			read_receipt_to, read_receipt_handled, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(folder_id, uid) DO UPDATE SET
			id=excluded.id, account_id=excluded.account_id,
			message_id=excluded.message_id, in_reply_to=excluded.in_reply_to,
			references_list=excluded.references_list, thread_id=excluded.thread_id,
			subject=excluded.subject, from_name=excluded.from_name, from_email=excluded.from_email,
			to_list=excluded.to_list, cc_list=excluded.cc_list, bcc_list=excluded.bcc_list,
			reply_to=excluded.reply_to, date=excluded.date,
			snippet=excluded.snippet, is_read=excluded.is_read, is_starred=excluded.is_starred,
			is_answered=excluded.is_answered, is_forwarded=excluded.is_forwarded,
			is_draft=excluded.is_draft, is_deleted=excluded.is_deleted,
			size=excluded.size, has_attachments=excluded.has_attachments,
			body_text=excluded.body_text, body_html=excluded.body_html,
			body_fetched=excluded.body_fetched,
			read_receipt_to=excluded.read_receipt_to, read_receipt_handled=excluded.read_receipt_handled,
			received_at=excluded.received_at
	`

	_, err := s.db.Exec(query,
		m.ID, m.AccountID, m.FolderID, m.UID,
		nullString(m.MessageID), nullString(m.InReplyTo), nullString(m.References), nullString(m.ThreadID),
		m.Subject, m.FromName, m.FromEmail,
		nullString(m.ToList), nullString(m.CcList), nullString(m.BccList), nullString(m.ReplyTo),
		m.Date, nullString(m.Snippet),
		m.IsRead, m.IsStarred, m.IsAnswered, m.IsForwarded, m.IsDraft, m.IsDeleted,
		m.Size, m.HasAttachments,
		nullString(m.BodyText), nullString(m.BodyHTML), m.BodyFetched,
		nullString(m.ReadReceiptTo), m.ReadReceiptHandled,
		m.ReceivedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert message: %w", err)
	}

	return nil
}

// Update updates an existing message
func (s *Store) Update(m *Message) error {
	query := `
		UPDATE messages SET
			message_id = ?, in_reply_to = ?, references_list = ?, thread_id = ?,
			subject = ?, from_name = ?, from_email = ?,
			to_list = ?, cc_list = ?, bcc_list = ?, reply_to = ?, date = ?,
			snippet = ?, is_read = ?, is_starred = ?, is_answered = ?, is_forwarded = ?,
			is_draft = ?, is_deleted = ?, size = ?, has_attachments = ?,
			body_text = ?, body_html = ?, read_receipt_to = ?, read_receipt_handled = ?
		WHERE id = ?
	`

	_, err := s.db.Exec(query,
		nullString(m.MessageID), nullString(m.InReplyTo), nullString(m.References), nullString(m.ThreadID),
		m.Subject, m.FromName, m.FromEmail,
		nullString(m.ToList), nullString(m.CcList), nullString(m.BccList), nullString(m.ReplyTo),
		m.Date, nullString(m.Snippet),
		m.IsRead, m.IsStarred, m.IsAnswered, m.IsForwarded,
		m.IsDraft, m.IsDeleted, m.Size, m.HasAttachments,
		nullString(m.BodyText), nullString(m.BodyHTML),
		nullString(m.ReadReceiptTo), m.ReadReceiptHandled,
		m.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update message: %w", err)
	}

	return nil
}

// UpdateFlags updates only the flags for a message
func (s *Store) UpdateFlags(id string, isRead, isStarred, isAnswered, isForwarded, isDraft, isDeleted bool) error {
	query := `
		UPDATE messages SET
			is_read = ?, is_starred = ?, is_answered = ?, is_forwarded = ?,
			is_draft = ?, is_deleted = ?
		WHERE id = ?
	`

	_, err := s.db.Exec(query, isRead, isStarred, isAnswered, isForwarded, isDraft, isDeleted, id)
	if err != nil {
		return fmt.Errorf("failed to update flags: %w", err)
	}

	return nil
}

// UpdateFlagsByUID updates flags for a message by folder ID and UID
func (s *Store) UpdateFlagsByUID(folderID string, uid uint32, isRead, isStarred, isAnswered, isForwarded, isDraft, isDeleted bool) error {
	query := `
		UPDATE messages SET
			is_read = ?, is_starred = ?, is_answered = ?, is_forwarded = ?,
			is_draft = ?, is_deleted = ?
		WHERE folder_id = ? AND uid = ?
	`

	_, err := s.db.Exec(query, isRead, isStarred, isAnswered, isForwarded, isDraft, isDeleted, folderID, uid)
	if err != nil {
		return fmt.Errorf("failed to update flags by UID: %w", err)
	}

	return nil
}

// FlagUpdate represents a flag update for a single message by UID
type FlagUpdate struct {
	UID         uint32
	IsRead      bool
	IsStarred   bool
	IsAnswered  bool
	IsForwarded bool
	IsDraft     bool
	IsDeleted   bool
}

// UpdateFlagsByUIDBatch updates flags for multiple messages in a single transaction.
// This is much more efficient than calling UpdateFlagsByUID repeatedly.
func (s *Store) UpdateFlagsByUIDBatch(folderID string, updates []FlagUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE messages SET
			is_read = ?, is_starred = ?, is_answered = ?, is_forwarded = ?,
			is_draft = ?, is_deleted = ?
		WHERE folder_id = ? AND uid = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, u := range updates {
		_, err := stmt.Exec(u.IsRead, u.IsStarred, u.IsAnswered, u.IsForwarded, u.IsDraft, u.IsDeleted, folderID, u.UID)
		if err != nil {
			return fmt.Errorf("failed to update flags for UID %d: %w", u.UID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// MarkReadReceiptHandled marks a message's read receipt as handled (sent or ignored)
func (s *Store) MarkReadReceiptHandled(id string) error {
	_, err := s.db.Exec("UPDATE messages SET read_receipt_handled = 1 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to mark read receipt handled: %w", err)
	}
	return nil
}

// Delete deletes a message
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	return nil
}

// DeleteByUID deletes a message by folder ID and UID
func (s *Store) DeleteByUID(folderID string, uid uint32) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE folder_id = ? AND uid = ?", folderID, uid)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	return nil
}

// DeleteByFolder deletes all messages in a folder
func (s *Store) DeleteByFolder(folderID string) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE folder_id = ?", folderID)
	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}
	return nil
}

// ExistsInFolder checks if a message with the given RFC 822 Message-ID exists
// in a folder of the specified type (e.g., "trash", "spam") for the account.
func (s *Store) ExistsInFolder(messageID string, folderType string, accountID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM messages m
		JOIN folders f ON m.folder_id = f.id
		WHERE m.message_id = ? AND f.folder_type = ? AND m.account_id = ?
	`, messageID, folderType, accountID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check message in folder type: %w", err)
	}
	return count > 0, nil
}

// HasCopiesInOtherFolders checks if a message with the same RFC 822 Message-ID
// exists in any other folder (excluding the specified folder) for the account.
// Used by Gmail-aware permanent delete to avoid destroying the underlying message
// when it's still visible in other labels.
func (s *Store) HasCopiesInOtherFolders(messageIDHeader string, excludeFolderID string, accountID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM messages
		WHERE message_id = ? AND folder_id != ? AND account_id = ? AND uid > 0
	`, messageIDHeader, excludeFolderID, accountID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check for copies: %w", err)
	}
	return count > 0, nil
}

// GetAllUIDs returns all UIDs for a folder
func (s *Store) GetAllUIDs(folderID string) ([]uint32, error) {
	rows, err := s.db.Query("SELECT uid FROM messages WHERE folder_id = ? AND uid > 0", folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query UIDs: %w", err)
	}
	defer rows.Close()

	var uids []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("failed to scan UID: %w", err)
		}
		uids = append(uids, uid)
	}

	return uids, nil
}

// GetHighestUID returns the highest UID in a folder
func (s *Store) GetHighestUID(folderID string) (uint32, error) {
	var uid sql.NullInt64
	err := s.db.QueryRow("SELECT MAX(uid) FROM messages WHERE folder_id = ?", folderID).Scan(&uid)
	if err != nil {
		return 0, fmt.Errorf("failed to get highest UID: %w", err)
	}
	if uid.Valid {
		return uint32(uid.Int64), nil
	}
	return 0, nil
}

// UpdateBody updates the body content of a message and marks it as fetched
func (s *Store) UpdateBody(messageID, bodyHTML, bodyText, snippet string, hasAttachments bool) error {
	query := `
		UPDATE messages
		SET body_html = ?, body_text = ?, snippet = ?, body_fetched = 1, has_attachments = ?
		WHERE id = ?
	`
	_, err := s.db.Exec(query, nullString(bodyHTML), nullString(bodyText), nullString(snippet), hasAttachments, messageID)
	if err != nil {
		return fmt.Errorf("failed to update body: %w", err)
	}
	return nil
}

// GetMessagesWithoutBody returns message IDs that don't have their body fetched yet
// GetMessagesWithoutBody returns message IDs that don't have their body fetched yet,
// or have body_fetched=1 but empty body content (self-healing for failed parses).
// If sinceDate is not zero, only returns messages dated on or after that date.
func (s *Store) GetMessagesWithoutBody(folderID string, limit int, sinceDate time.Time) ([]string, error) {
	var query string
	var rows *sql.Rows
	var err error

	// Include messages where body_fetched=0 OR body was fetched but is empty (needs re-fetch)
	// Exclude encrypted messages which intentionally have empty body (decrypted on-view)
	if sinceDate.IsZero() {
		query = `
			SELECT id FROM messages
			WHERE folder_id = ? AND (
				body_fetched = 0 OR
				(body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))
			)
			ORDER BY date DESC
			LIMIT ?
		`
		rows, err = s.db.Query(query, folderID, limit)
	} else {
		query = `
			SELECT id FROM messages
			WHERE folder_id = ? AND (
				body_fetched = 0 OR
				(body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))
			) AND (date >= ? OR date < '1970-01-01')
			ORDER BY date DESC
			LIMIT ?
		`
		rows, err = s.db.Query(query, folderID, sinceDate, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query messages without body: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan message id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// MessageWithSize holds a message ID and its RFC822 size for batch planning
type MessageWithSize struct {
	ID   string
	Size int
}

// GetMessagesWithoutBodyAndSize returns message IDs and sizes that don't have their body fetched yet,
// ordered by date descending (newest first). Used for byte-aware batch planning.
// If sinceDate is not zero, only returns messages dated on or after that date.
func (s *Store) GetMessagesWithoutBodyAndSize(folderID string, limit int, sinceDate time.Time) ([]MessageWithSize, error) {
	var query string
	var rows *sql.Rows
	var err error

	// Include messages where body_fetched=0 OR body was fetched but is empty (needs re-fetch)
	// Exclude encrypted messages which intentionally have empty body (decrypted on-view)
	if sinceDate.IsZero() {
		query = `
			SELECT id, size FROM messages
			WHERE folder_id = ? AND (
				body_fetched = 0 OR
				(body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))
			)
			ORDER BY date DESC
			LIMIT ?
		`
		rows, err = s.db.Query(query, folderID, limit)
	} else {
		query = `
			SELECT id, size FROM messages
			WHERE folder_id = ? AND (
				body_fetched = 0 OR
				(body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))
			) AND (date >= ? OR date < '1970-01-01')
			ORDER BY date DESC
			LIMIT ?
		`
		rows, err = s.db.Query(query, folderID, sinceDate, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to query messages without body: %w", err)
	}
	defer rows.Close()

	var messages []MessageWithSize
	for rows.Next() {
		var msg MessageWithSize
		if err := rows.Scan(&msg.ID, &msg.Size); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// CountMessagesWithoutBody returns the count of messages that don't have their body fetched,
// or have body_fetched=1 but empty body content (self-healing for failed parses).
// If sinceDate is not zero, only counts messages dated on or after that date.
func (s *Store) CountMessagesWithoutBody(folderID string, sinceDate time.Time) (int, error) {
	var count int
	var err error

	// Include messages where body_fetched=0 OR body was fetched but is empty (needs re-fetch)
	// Exclude encrypted messages which intentionally have empty body (decrypted on-view)
	if sinceDate.IsZero() {
		err = s.db.QueryRow(
			`SELECT COUNT(*) FROM messages WHERE folder_id = ? AND (
				body_fetched = 0 OR
				(body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))
			)`,
			folderID,
		).Scan(&count)
	} else {
		err = s.db.QueryRow(
			`SELECT COUNT(*) FROM messages WHERE folder_id = ? AND (
				body_fetched = 0 OR
				(body_fetched = 1 AND smime_encrypted = 0 AND pgp_encrypted = 0 AND (body_text IS NULL OR body_text = '') AND (body_html IS NULL OR body_html = ''))
			) AND (date >= ? OR date < '1970-01-01')`,
			folderID, sinceDate,
		).Scan(&count)
	}

	if err != nil {
		return 0, fmt.Errorf("failed to count messages without body: %w", err)
	}
	return count, nil
}

// CountByFolder returns the total message count for a folder
func (s *Store) CountByFolder(folderID string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE folder_id = ?", folderID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count messages: %w", err)
	}
	return count, nil
}

// DeleteOlderThan deletes messages older than the specified time for an account
// Returns the number of messages deleted
func (s *Store) DeleteOlderThan(accountID string, before time.Time) (int, error) {
	result, err := s.db.Exec(
		"DELETE FROM messages WHERE account_id = ? AND date < ?",
		accountID, before,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to delete old messages: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get affected rows: %w", err)
	}

	if affected > 0 {
		s.log.Info().
			Str("accountID", accountID).
			Time("before", before).
			Int64("deleted", affected).
			Msg("Deleted old messages based on sync period")
	}

	return int(affected), nil
}

// GetMessageUIDAndFolder returns the UID and folder_id for a message
func (s *Store) GetMessageUIDAndFolder(messageID string) (uint32, string, error) {
	var uid uint32
	var folderID string
	err := s.db.QueryRow(
		"SELECT uid, folder_id FROM messages WHERE id = ?",
		messageID,
	).Scan(&uid, &folderID)
	if err == sql.ErrNoRows {
		return 0, "", fmt.Errorf("message not found: %s", messageID)
	}
	if err != nil {
		return 0, "", fmt.Errorf("failed to get message: %w", err)
	}
	return uid, folderID, nil
}

// UIDInfo holds UID and folder information for a message
type UIDInfo struct {
	UID      uint32
	FolderID string
}

// GetMessageUIDsAndFolder returns UIDs and folder_ids for multiple messages in one query
func (s *Store) GetMessageUIDsAndFolder(messageIDs []string) (map[string]UIDInfo, error) {
	if len(messageIDs) == 0 {
		return make(map[string]UIDInfo), nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(messageIDs))
	args := make([]interface{}, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		"SELECT id, uid, folder_id FROM messages WHERE id IN (%s)",
		strings.Join(placeholders, ", "),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query message UIDs: %w", err)
	}
	defer rows.Close()

	result := make(map[string]UIDInfo)
	for rows.Next() {
		var id string
		var uid uint32
		var folderID string
		if err := rows.Scan(&id, &uid, &folderID); err != nil {
			return nil, fmt.Errorf("failed to scan message UID: %w", err)
		}
		result[id] = UIDInfo{UID: uid, FolderID: folderID}
	}

	return result, nil
}

// BodyUpdate holds body content for batch updates
type BodyUpdate struct {
	MessageID          string
	BodyHTML           string
	BodyText           string
	Snippet            string
	HasAttachments     bool
	SMIMEStatus        string
	SMIMESignerEmail   string
	SMIMESignerSubject string
	SMIMERawBody       []byte
	SMIMEEncrypted     bool
	PGPRawBody         []byte
	PGPEncrypted       bool
}

// UpdateBodiesBatch updates body content for multiple messages in a single transaction
func (s *Store) UpdateBodiesBatch(updates []BodyUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE messages
		SET body_html = ?, body_text = ?, snippet = ?, body_fetched = 1,
		    has_attachments = ?,
		    smime_status = ?, smime_signer_email = ?, smime_signer_subject = ?,
		    smime_raw_body = ?, smime_encrypted = ?,
		    pgp_raw_body = ?, pgp_encrypted = ?
		WHERE id = ?
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, u := range updates {
		var smimeRawBody interface{}
		if len(u.SMIMERawBody) > 0 {
			smimeRawBody = u.SMIMERawBody
		}
		var pgpRawBody interface{}
		if len(u.PGPRawBody) > 0 {
			pgpRawBody = u.PGPRawBody
		}
		_, err := stmt.Exec(
			nullString(u.BodyHTML), nullString(u.BodyText), nullString(u.Snippet),
			u.HasAttachments,
			nullString(u.SMIMEStatus), nullString(u.SMIMESignerEmail), nullString(u.SMIMESignerSubject),
			smimeRawBody, u.SMIMEEncrypted,
			pgpRawBody, u.PGPEncrypted,
			u.MessageID,
		)
		if err != nil {
			s.log.Warn().Err(err).Str("messageID", u.MessageID).Msg("Failed to update body in batch")
			// Continue with other updates
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetSMIMERawBody returns the raw S/MIME body bytes for a message (for on-view decryption/verification)
func (s *Store) GetSMIMERawBody(messageID string) ([]byte, error) {
	var rawBody []byte
	err := s.db.QueryRow("SELECT smime_raw_body FROM messages WHERE id = ?", messageID).Scan(&rawBody)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get S/MIME raw body: %w", err)
	}
	return rawBody, nil
}

// GetPGPRawBody returns the raw PGP body bytes for a message (for on-view decryption/verification)
func (s *Store) GetPGPRawBody(messageID string) ([]byte, error) {
	var rawBody []byte
	err := s.db.QueryRow("SELECT pgp_raw_body FROM messages WHERE id = ?", messageID).Scan(&rawBody)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get PGP raw body: %w", err)
	}
	return rawBody, nil
}

// ClearBodiesForFolder clears body content for all messages in a folder.
// This resets body_html, body_text, snippet to NULL and body_fetched to 0,
// allowing the messages to be re-fetched and re-parsed during the next body sync.
func (s *Store) ClearBodiesForFolder(folderID string) (int64, error) {
	query := `
		UPDATE messages
		SET body_html = NULL, body_text = NULL, snippet = NULL, body_fetched = 0
		WHERE folder_id = ?
	`
	result, err := s.db.Exec(query, folderID)
	if err != nil {
		return 0, fmt.Errorf("failed to clear bodies for folder: %w", err)
	}

	affected, _ := result.RowsAffected()
	s.log.Info().Str("folderID", folderID).Int64("affected", affected).Msg("Cleared bodies for folder")
	return affected, nil
}

// helper to convert empty string to NULL
func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// parseTimeString parses a time string in various formats
func parseTimeString(s string) time.Time {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05 -0700 MST", // Format used by Go's time.Time.String() when stored in SQLite
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, s); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// ListConversationsByFolder returns conversations (grouped by thread) for a folder with pagination
// sortOrder can be "newest" (default) or "oldest"
func (s *Store) ListConversationsByFolder(folderID string, offset, limit int, sortOrder, filter string) ([]*Conversation, error) {
	// Determine sort direction
	orderClause := "ORDER BY latest_date DESC"
	if sortOrder == "oldest" {
		orderClause = "ORDER BY latest_date ASC"
	}

	// Get conversations grouped by thread_id, ordered by date
	// Use GROUP_CONCAT to get all message IDs in a single query
	query := `
		SELECT 
			COALESCE(thread_id, id) as conv_thread_id,
			MIN(subject) as subject,
			MAX(snippet) as snippet,
			COUNT(*) as message_count,
			SUM(CASE WHEN is_read = 0 THEN 1 ELSE 0 END) as unread_count,
			MAX(CASE WHEN has_attachments = 1 THEN 1 ELSE 0 END) as has_attachments,
			MAX(CASE WHEN is_starred = 1 THEN 1 ELSE 0 END) as is_starred,
			MAX(date) as latest_date,
			GROUP_CONCAT(id) as message_ids,
			MAX(CASE WHEN smime_encrypted = 1 OR pgp_encrypted = 1 THEN 1 ELSE 0 END) as is_encrypted
		FROM messages
		WHERE folder_id = ?
		GROUP BY COALESCE(thread_id, id)` +
		filterHavingClause(filter, "") + `
		` + orderClause + `
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.Query(query, folderID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversations: %w", err)
	}
	defer rows.Close()

	var conversations []*Conversation
	for rows.Next() {
		c := &Conversation{}
		var latestDateStr sql.NullString
		var snippet sql.NullString
		var messageIDsStr sql.NullString

		err := rows.Scan(
			&c.ThreadID,
			&c.Subject,
			&snippet,
			&c.MessageCount,
			&c.UnreadCount,
			&c.HasAttachments,
			&c.IsStarred,
			&latestDateStr,
			&messageIDsStr,
			&c.IsEncrypted,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan conversation: %w", err)
		}

		if snippet.Valid {
			c.Snippet = snippet.String
		}
		if latestDateStr.Valid && latestDateStr.String != "" {
			c.LatestDate = parseTimeString(latestDateStr.String)
		}

		// Parse message IDs from comma-separated string
		if messageIDsStr.Valid && messageIDsStr.String != "" {
			c.MessageIDs = strings.Split(messageIDsStr.String, ",")
		}

		// Get participants for this conversation
		participants, err := s.getConversationParticipants(c.ThreadID, folderID)
		if err != nil {
			s.log.Warn().Err(err).Str("threadId", c.ThreadID).Msg("Failed to get participants")
		}
		c.Participants = participants

		conversations = append(conversations, c)
	}

	return conversations, nil
}

// getConversationParticipants returns unique participants in a conversation
func (s *Store) getConversationParticipants(threadID, folderID string) ([]Address, error) {
	query := `
		SELECT DISTINCT from_name, from_email
		FROM messages
		WHERE folder_id = ? AND COALESCE(thread_id, id) = ?
		ORDER BY date ASC
	`

	rows, err := s.db.Query(query, folderID, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []Address
	seen := make(map[string]bool)

	for rows.Next() {
		var name, email string
		if err := rows.Scan(&name, &email); err != nil {
			continue
		}
		if !seen[email] {
			seen[email] = true
			participants = append(participants, Address{Name: name, Email: email})
		}
	}

	return participants, nil
}

// CountConversationsByFolder returns the count of conversations in a folder
func (s *Store) CountConversationsByFolder(folderID, filter string) (int, error) {
	query := `
		SELECT COUNT(DISTINCT COALESCE(thread_id, id))
		FROM messages
		WHERE folder_id = ?
	` + filterWhereClause(filter, "")

	var count int
	err := s.db.QueryRow(query, folderID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count conversations: %w", err)
	}
	return count, nil
}

// GetConversation returns messages in a conversation/thread from the specified folder plus Sent and Drafts
func (s *Store) GetConversation(threadID, folderID string) (*Conversation, error) {
	s.log.Debug().
		Str("threadID", threadID).
		Str("folderID", folderID).
		Msg("GetConversation called in store")

	// Check if the threadID is a message ID (UUID) first
	// If the threadID looks like a UUID (no angle brackets), check if it's a message ID directly
	if !strings.Contains(threadID, "@") && !strings.HasPrefix(threadID, "<") {
		// This might be a message UUID, check if we can find a message with this ID
		var count int
		err := s.db.QueryRow("SELECT COUNT(*) FROM messages WHERE id = ?", threadID).Scan(&count)
		if err == nil && count > 0 {
			s.log.Debug().Str("threadID", threadID).Msg("ThreadID is a message UUID")
		}
	}

	// First get the account ID and folder type
	var accountID string
	var folderType string
	err := s.db.QueryRow("SELECT account_id, folder_type FROM folders WHERE id = ?", folderID).Scan(&accountID, &folderType)
	if err != nil {
		return nil, fmt.Errorf("failed to get account ID and folder type: %w", err)
	}

	// Normalize the thread ID for comparison
	normalizedThreadID := normalizeMessageID(threadID)
	s.log.Debug().
		Str("normalizedThreadID", normalizedThreadID).
		Str("accountID", accountID).
		Msg("GetConversation normalized")

	// Get conversation summary from current folder + Sent + Drafts
	// This gives full conversation context without cross-folder bleed
	// Exclude messages in Trash folder unless we're viewing Trash
	// Use COALESCE to handle NULL values from aggregate functions when no rows match
	trashFilter := ""
	if folderType != "trash" {
		trashFilter = "AND f.folder_type != 'trash'"
	}

	// Scope to current folder + Sent + Drafts (for full conversation context)
	folderFilter := "AND (m.folder_id = ? OR f.folder_type IN ('sent', 'drafts'))"

	summaryQuery := fmt.Sprintf(`
		SELECT
			COALESCE(MIN(m.subject), '') as subject,
			COALESCE(MAX(m.snippet), '') as snippet,
			COUNT(*) as message_count,
			COALESCE(SUM(CASE WHEN m.is_read = 0 THEN 1 ELSE 0 END), 0) as unread_count,
			COALESCE(MAX(CASE WHEN m.has_attachments = 1 THEN 1 ELSE 0 END), 0) as has_attachments,
			COALESCE(MAX(CASE WHEN m.is_starred = 1 THEN 1 ELSE 0 END), 0) as is_starred,
			MAX(m.date) as latest_date
		FROM messages m
		INNER JOIN folders f ON m.folder_id = f.id
		WHERE m.account_id = ? AND (
			REPLACE(REPLACE(COALESCE(m.thread_id, m.id), '<', ''), '>', '') = ?
			OR REPLACE(REPLACE(m.message_id, '<', ''), '>', '') = ?
			OR REPLACE(REPLACE(m.in_reply_to, '<', ''), '>', '') = ?
		)
		%s %s
	`, trashFilter, folderFilter)

	c := &Conversation{ThreadID: threadID}
	var latestDateStr sql.NullString

	err = s.db.QueryRow(summaryQuery, accountID, normalizedThreadID, normalizedThreadID, normalizedThreadID, folderID).Scan(
		&c.Subject,
		&c.Snippet,
		&c.MessageCount,
		&c.UnreadCount,
		&c.HasAttachments,
		&c.IsStarred,
		&latestDateStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation summary: %w", err)
	}
	if latestDateStr.Valid && latestDateStr.String != "" {
		c.LatestDate = parseTimeString(latestDateStr.String)
	}

	// Get messages in the thread from current folder + Sent + Drafts
	// This gives full conversation context without cross-folder bleed
	// Exclude messages in Trash folder unless we're viewing Trash
	messagesQuery := fmt.Sprintf(`
		SELECT m.id, m.account_id, m.folder_id, m.uid, m.message_id, m.in_reply_to, m.references_list, m.thread_id,
		       m.subject, m.from_name, m.from_email, m.to_list, m.cc_list, m.bcc_list, m.reply_to, m.date,
		       m.snippet, m.is_read, m.is_starred, m.is_answered, m.is_forwarded, m.is_draft, m.is_deleted,
		       m.size, m.has_attachments, m.body_text, m.body_html, m.body_fetched,
		       m.read_receipt_to, m.read_receipt_handled,
		       m.smime_status, m.smime_signer_email, m.smime_signer_subject,
		       m.smime_encrypted, (m.smime_raw_body IS NOT NULL) as has_smime,
		       m.pgp_status, m.pgp_signer_email, m.pgp_signer_key_id,
		       m.pgp_encrypted, (m.pgp_raw_body IS NOT NULL) as has_pgp,
		       m.received_at
		FROM messages m
		INNER JOIN folders f ON m.folder_id = f.id
		WHERE m.account_id = ? AND (
			REPLACE(REPLACE(COALESCE(m.thread_id, m.id), '<', ''), '>', '') = ?
			OR REPLACE(REPLACE(m.message_id, '<', ''), '>', '') = ?
			OR REPLACE(REPLACE(m.in_reply_to, '<', ''), '>', '') = ?
		)
		%s %s
		ORDER BY m.date ASC
	`, trashFilter, folderFilter)

	rows, err := s.db.Query(messagesQuery, accountID, normalizedThreadID, normalizedThreadID, normalizedThreadID, folderID)
	if err != nil {
		return nil, fmt.Errorf("failed to query thread messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		m := &Message{}
		var messageID, inReplyTo, references, threadIDVal, toList, ccList, bccList, replyTo, snippetVal, bodyText, bodyHTML, readReceiptTo sql.NullString
		var smimeStatus, smimeSignerEmail, smimeSignerSubject sql.NullString
		var pgpStatus, pgpSignerEmail, pgpSignerKeyID sql.NullString
		var dateStr, receivedAtStr sql.NullString

		err := rows.Scan(
			&m.ID, &m.AccountID, &m.FolderID, &m.UID, &messageID, &inReplyTo, &references, &threadIDVal,
			&m.Subject, &m.FromName, &m.FromEmail, &toList, &ccList, &bccList, &replyTo, &dateStr,
			&snippetVal, &m.IsRead, &m.IsStarred, &m.IsAnswered, &m.IsForwarded, &m.IsDraft, &m.IsDeleted,
			&m.Size, &m.HasAttachments, &bodyText, &bodyHTML, &m.BodyFetched,
			&readReceiptTo, &m.ReadReceiptHandled,
			&smimeStatus, &smimeSignerEmail, &smimeSignerSubject,
			&m.SMIMEEncrypted, &m.HasSMIME,
			&pgpStatus, &pgpSignerEmail, &pgpSignerKeyID,
			&m.PGPEncrypted, &m.HasPGP,
			&receivedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		if messageID.Valid {
			m.MessageID = messageID.String
		}
		if inReplyTo.Valid {
			m.InReplyTo = inReplyTo.String
		}
		if references.Valid {
			m.References = references.String
		}
		if threadIDVal.Valid {
			m.ThreadID = threadIDVal.String
		}
		if toList.Valid {
			m.ToList = toList.String
		}
		if ccList.Valid {
			m.CcList = ccList.String
		}
		if bccList.Valid {
			m.BccList = bccList.String
		}
		if replyTo.Valid {
			m.ReplyTo = replyTo.String
		}
		if dateStr.Valid && dateStr.String != "" {
			m.Date = parseTimeString(dateStr.String)
		}
		if snippetVal.Valid {
			m.Snippet = snippetVal.String
		}
		if bodyText.Valid {
			m.BodyText = bodyText.String
		}
		if bodyHTML.Valid {
			m.BodyHTML = bodyHTML.String
		}
		if readReceiptTo.Valid {
			m.ReadReceiptTo = readReceiptTo.String
		}
		if smimeStatus.Valid {
			m.SMIMEStatus = smimeStatus.String
		}
		if smimeSignerEmail.Valid {
			m.SMIMESignerEmail = smimeSignerEmail.String
		}
		if smimeSignerSubject.Valid {
			m.SMIMESignerSubject = smimeSignerSubject.String
		}
		if pgpStatus.Valid {
			m.PGPStatus = pgpStatus.String
		}
		if pgpSignerEmail.Valid {
			m.PGPSignerEmail = pgpSignerEmail.String
		}
		if pgpSignerKeyID.Valid {
			m.PGPSignerKeyID = pgpSignerKeyID.String
		}
		if receivedAtStr.Valid && receivedAtStr.String != "" {
			m.ReceivedAt = parseTimeString(receivedAtStr.String)
		}

		s.log.Debug().
			Str("id", m.ID).
			Str("messageID", m.MessageID).
			Str("threadID", m.ThreadID).
			Str("subject", m.Subject).
			Int("bodyTextLen", len(m.BodyText)).
			Int("bodyHTMLLen", len(m.BodyHTML)).
			Msg("GetConversation found message")

		c.Messages = append(c.Messages, m)
	}

	s.log.Debug().
		Int("messageCount", len(c.Messages)).
		Str("threadID", threadID).
		Msg("GetConversation returning")

	// Get participants
	c.Participants, _ = s.getConversationParticipants(threadID, folderID)

	return c, nil
}

// normalizeMessageID strips angle brackets from Message-IDs for consistent comparison
func normalizeMessageID(msgID string) string {
	msgID = strings.TrimSpace(msgID)
	msgID = strings.TrimPrefix(msgID, "<")
	msgID = strings.TrimSuffix(msgID, ">")
	return msgID
}

// FindThreadID finds the thread ID for a message based on References and In-Reply-To headers
func (s *Store) FindThreadID(accountID, messageID, inReplyTo string, references []string) (string, error) {
	// Normalize the message ID
	messageID = normalizeMessageID(messageID)
	inReplyTo = normalizeMessageID(inReplyTo)

	// Normalize all references
	normalizedRefs := make([]string, 0, len(references))
	for _, ref := range references {
		if normalized := normalizeMessageID(ref); normalized != "" {
			normalizedRefs = append(normalizedRefs, normalized)
		}
	}

	// Build list of all potential thread roots to check
	allRefs := make([]string, 0)
	if inReplyTo != "" {
		allRefs = append(allRefs, inReplyTo)
	}
	allRefs = append(allRefs, normalizedRefs...)

	if len(allRefs) == 0 {
		// No threading info - this message starts its own thread
		return messageID, nil
	}

	// Check if any of the references match existing messages
	for _, ref := range allRefs {
		// Check with and without angle brackets since DB might have either format
		var existingThreadID sql.NullString

		// Try exact match first
		err := s.db.QueryRow(
			"SELECT COALESCE(thread_id, id) FROM messages WHERE account_id = ? AND (message_id = ? OR message_id = ? OR message_id = ?) LIMIT 1",
			accountID, ref, "<"+ref+">", strings.TrimPrefix(strings.TrimSuffix(ref, ">"), "<"),
		).Scan(&existingThreadID)

		if err == nil && existingThreadID.Valid && existingThreadID.String != "" {
			// Normalize the returned thread ID too
			return normalizeMessageID(existingThreadID.String), nil
		}
	}

	// No existing thread found - use the first reference as thread ID (root message)
	// This is the original message that started the thread
	if len(normalizedRefs) > 0 {
		return normalizedRefs[0], nil
	}
	if inReplyTo != "" {
		return inReplyTo, nil
	}

	return messageID, nil
}

// UpdateThreadID updates the thread_id for a message
func (s *Store) UpdateThreadID(id, threadID string) error {
	_, err := s.db.Exec("UPDATE messages SET thread_id = ? WHERE id = ?", threadID, id)
	if err != nil {
		return fmt.Errorf("failed to update thread_id: %w", err)
	}
	return nil
}

// ReconcileThreads updates thread_ids for messages that reference a newly synced message.
// This ensures that when a new message arrives, any existing messages that reference it
// (via In-Reply-To) get linked to the same thread.
// Returns the number of messages updated.
func (s *Store) ReconcileThreads(accountID, messageID, threadID string) (int, error) {
	// Normalize the message ID for comparison
	normalizedMsgID := normalizeMessageID(messageID)
	if normalizedMsgID == "" {
		return 0, nil
	}

	// Find messages that have in_reply_to pointing to this message's ID
	// and update their thread_id to match
	// We check multiple formats of the message ID (with/without angle brackets)
	query := `
		UPDATE messages 
		SET thread_id = ?
		WHERE account_id = ? 
		AND thread_id != ?
		AND (
			REPLACE(REPLACE(in_reply_to, '<', ''), '>', '') = ?
			OR in_reply_to = ?
			OR in_reply_to = ?
		)
	`

	result, err := s.db.Exec(query,
		threadID,
		accountID,
		threadID,
		normalizedMsgID,
		normalizedMsgID,
		"<"+normalizedMsgID+">",
	)
	if err != nil {
		return 0, fmt.Errorf("failed to reconcile threads: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		s.log.Info().
			Str("accountID", accountID).
			Str("messageID", messageID).
			Str("threadID", threadID).
			Int64("updated", affected).
			Msg("Reconciled thread IDs for related messages")
	}

	return int(affected), nil
}

// ReconcileThreadsForNewMessage is called after syncing a new message.
// It checks both directions:
// 1. If this message references other messages, update this message's thread_id to match
// 2. If other messages reference this message's ID, update their thread_ids to match this one
func (s *Store) ReconcileThreadsForNewMessage(accountID, messageUUID, messageID, threadID, inReplyTo string) error {
	normalizedMsgID := normalizeMessageID(messageID)
	normalizedThreadID := normalizeMessageID(threadID)
	normalizedInReplyTo := normalizeMessageID(inReplyTo)

	// Direction 1: This message replies to another - find the original and adopt its thread_id
	if normalizedInReplyTo != "" {
		var existingThreadID sql.NullString
		err := s.db.QueryRow(`
			SELECT COALESCE(thread_id, id) FROM messages 
			WHERE account_id = ? 
			AND (
				REPLACE(REPLACE(message_id, '<', ''), '>', '') = ?
				OR message_id = ?
				OR message_id = ?
			)
			LIMIT 1
		`, accountID, normalizedInReplyTo, normalizedInReplyTo, "<"+normalizedInReplyTo+">").Scan(&existingThreadID)

		if err == nil && existingThreadID.Valid && existingThreadID.String != "" {
			existingNormalized := normalizeMessageID(existingThreadID.String)
			if existingNormalized != normalizedThreadID {
				// Update this message's thread_id to match the existing thread
				if err := s.UpdateThreadID(messageUUID, existingNormalized); err != nil {
					s.log.Warn().Err(err).Msg("Failed to update thread_id for reply")
				} else {
					s.log.Debug().
						Str("messageUUID", messageUUID).
						Str("oldThreadID", normalizedThreadID).
						Str("newThreadID", existingNormalized).
						Msg("Updated reply message to join existing thread")
					normalizedThreadID = existingNormalized
				}
			}
		}
	}

	// Direction 2: Other messages may have replied to this one - update their thread_ids
	if normalizedMsgID != "" {
		_, err := s.ReconcileThreads(accountID, normalizedMsgID, normalizedThreadID)
		if err != nil {
			s.log.Warn().Err(err).Msg("Failed to reconcile threads for replies to this message")
		}
	}

	return nil
}

// UpdateFlagsBatch updates flags for multiple messages by their IDs
// Pass nil for flags you don't want to update
func (s *Store) UpdateFlagsBatch(ids []string, isRead, isStarred *bool) error {
	if len(ids) == 0 {
		return nil
	}

	// Build dynamic SET clause based on what's being updated
	var setClauses []string
	var args []interface{}

	if isRead != nil {
		setClauses = append(setClauses, "is_read = ?")
		args = append(args, *isRead)
	}
	if isStarred != nil {
		setClauses = append(setClauses, "is_starred = ?")
		args = append(args, *isStarred)
	}

	if len(setClauses) == 0 {
		return nil
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		"UPDATE messages SET %s WHERE id IN (%s)",
		strings.Join(setClauses, ", "),
		strings.Join(placeholders, ", "),
	)

	_, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update flags batch: %w", err)
	}
	return nil
}

// MoveMessages updates the folder_id for multiple messages
func (s *Store) MoveMessages(ids []string, newFolderID string) error {
	if len(ids) == 0 {
		return nil
	}

	// Deduplicate message IDs to prevent constraint violations
	seen := make(map[string]bool)
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	placeholders := make([]string, len(uniqueIDs))
	args := []interface{}{newFolderID}
	for i, id := range uniqueIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(
		"UPDATE messages SET folder_id = ?, uid = -rowid WHERE id IN (%s)",
		strings.Join(placeholders, ", "),
	)

	_, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to move messages: %w", err)
	}
	return nil
}

// DeleteTempUIDs removes messages with temporary negative UIDs in a folder.
// These are left over after MoveMessages assigns -rowid as a placeholder UID.
func (s *Store) DeleteTempUIDs(folderID string) error {
	_, err := s.db.Exec("DELETE FROM messages WHERE folder_id = ? AND uid < 0", folderID)
	if err != nil {
		return fmt.Errorf("failed to delete temp UID messages: %w", err)
	}
	return nil
}

// GetIDsByMessageIDs finds local DB message IDs by RFC822 Message-ID header and folder.
// Only returns messages with positive UIDs (excludes temp rows from in-flight moves).
func (s *Store) GetIDsByMessageIDs(accountID, folderID string, rfc822MessageIDs []string) ([]string, error) {
	if len(rfc822MessageIDs) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(rfc822MessageIDs))
	args := []interface{}{accountID, folderID}
	for i, mid := range rfc822MessageIDs {
		placeholders[i] = "?"
		args = append(args, mid)
	}

	query := fmt.Sprintf(
		"SELECT id FROM messages WHERE account_id = ? AND folder_id = ? AND uid > 0 AND message_id IN (%s)",
		strings.Join(placeholders, ", "),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan message ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteBatch deletes multiple messages by their IDs
func (s *Store) DeleteBatch(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM messages WHERE id IN (%s)", strings.Join(placeholders, ", "))
	_, err := s.db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete messages batch: %w", err)
	}
	return nil
}

// GetByIDs retrieves multiple messages by their IDs
func (s *Store) GetByIDs(ids []string) ([]*Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, account_id, folder_id, uid, message_id, in_reply_to, references_list, thread_id,
		       subject, from_name, from_email, to_list, cc_list, bcc_list, reply_to, date,
		       snippet, is_read, is_starred, is_answered, is_forwarded, is_draft, is_deleted,
		       size, has_attachments, body_text, body_html, body_fetched,
		       read_receipt_to, read_receipt_handled,
		       smime_status, smime_signer_email, smime_signer_subject,
		       smime_encrypted, (smime_raw_body IS NOT NULL) as has_smime,
		       pgp_status, pgp_signer_email, pgp_signer_key_id,
		       pgp_encrypted, (pgp_raw_body IS NOT NULL) as has_pgp,
		       received_at
		FROM messages WHERE id IN (%s)
	`, strings.Join(placeholders, ", "))

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var messages []*Message
	for rows.Next() {
		m := &Message{}
		var messageID, inReplyTo, references, threadID, toList, ccList, bccList, replyTo, snippet, bodyText, bodyHTML, readReceiptTo sql.NullString
		var smimeStatus, smimeSignerEmail, smimeSignerSubject sql.NullString
		var pgpStatus, pgpSignerEmail, pgpSignerKeyID sql.NullString
		var dateStr, receivedAtStr sql.NullString

		err := rows.Scan(
			&m.ID, &m.AccountID, &m.FolderID, &m.UID, &messageID, &inReplyTo, &references, &threadID,
			&m.Subject, &m.FromName, &m.FromEmail, &toList, &ccList, &bccList, &replyTo, &dateStr,
			&snippet, &m.IsRead, &m.IsStarred, &m.IsAnswered, &m.IsForwarded, &m.IsDraft, &m.IsDeleted,
			&m.Size, &m.HasAttachments, &bodyText, &bodyHTML, &m.BodyFetched,
			&readReceiptTo, &m.ReadReceiptHandled,
			&smimeStatus, &smimeSignerEmail, &smimeSignerSubject,
			&m.SMIMEEncrypted, &m.HasSMIME,
			&pgpStatus, &pgpSignerEmail, &pgpSignerKeyID,
			&m.PGPEncrypted, &m.HasPGP,
			&receivedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		if messageID.Valid {
			m.MessageID = messageID.String
		}
		if inReplyTo.Valid {
			m.InReplyTo = inReplyTo.String
		}
		if references.Valid {
			m.References = references.String
		}
		if threadID.Valid {
			m.ThreadID = threadID.String
		}
		if toList.Valid {
			m.ToList = toList.String
		}
		if ccList.Valid {
			m.CcList = ccList.String
		}
		if bccList.Valid {
			m.BccList = bccList.String
		}
		if replyTo.Valid {
			m.ReplyTo = replyTo.String
		}
		if dateStr.Valid && dateStr.String != "" {
			m.Date = parseTimeString(dateStr.String)
		}
		if snippet.Valid {
			m.Snippet = snippet.String
		}
		if bodyText.Valid {
			m.BodyText = bodyText.String
		}
		if bodyHTML.Valid {
			m.BodyHTML = bodyHTML.String
		}
		if readReceiptTo.Valid {
			m.ReadReceiptTo = readReceiptTo.String
		}
		if smimeStatus.Valid {
			m.SMIMEStatus = smimeStatus.String
		}
		if smimeSignerEmail.Valid {
			m.SMIMESignerEmail = smimeSignerEmail.String
		}
		if smimeSignerSubject.Valid {
			m.SMIMESignerSubject = smimeSignerSubject.String
		}
		if pgpStatus.Valid {
			m.PGPStatus = pgpStatus.String
		}
		if pgpSignerEmail.Valid {
			m.PGPSignerEmail = pgpSignerEmail.String
		}
		if pgpSignerKeyID.Valid {
			m.PGPSignerKeyID = pgpSignerKeyID.String
		}
		if receivedAtStr.Valid && receivedAtStr.String != "" {
			m.ReceivedAt = parseTimeString(receivedAtStr.String)
		}

		messages = append(messages, m)
	}

	return messages, nil
}

// SearchConversations searches for conversations in a folder using FTS5
// Returns conversations with highlighted text and the total count
func (s *Store) SearchConversations(folderID, query string, offset, limit int, filter string) ([]*ConversationSearchResult, int, error) {
	if query == "" {
		return nil, 0, nil
	}

	// Prepare the FTS query - escape special characters and add prefix matching
	ftsQuery := prepareFTSQuery(query)

	// First, get the total count
	countQuery := `
		SELECT COUNT(DISTINCT COALESCE(m.thread_id, m.id))
		FROM messages m
		JOIN messages_fts fts ON m.rowid = fts.rowid
		WHERE m.folder_id = ? AND messages_fts MATCH ?
	` + filterWhereClause(filter, "m.")
	var totalCount int
	err := s.db.QueryRow(countQuery, folderID, ftsQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count search results: %w", err)
	}

	if totalCount == 0 {
		return nil, 0, nil
	}

	// Get folder info for displaying in results
	var folderName, folderType string
	err = s.db.QueryRow("SELECT name, folder_type FROM folders WHERE id = ?", folderID).Scan(&folderName, &folderType)
	if err != nil {
		folderName = "Unknown"
		folderType = "folder"
	}

	// Get matching conversations with relevance ranking
	searchQuery := `
		SELECT 
			COALESCE(m.thread_id, m.id) as conv_thread_id,
			MIN(m.subject) as subject,
			MAX(m.snippet) as snippet,
			MIN(m.from_name) as from_name,
			COUNT(*) as message_count,
			SUM(CASE WHEN m.is_read = 0 THEN 1 ELSE 0 END) as unread_count,
			MAX(CASE WHEN m.has_attachments = 1 THEN 1 ELSE 0 END) as has_attachments,
			MAX(CASE WHEN m.is_starred = 1 THEN 1 ELSE 0 END) as is_starred,
			MAX(m.date) as latest_date,
			GROUP_CONCAT(m.id) as message_ids,
			MAX(CASE WHEN m.smime_encrypted = 1 OR m.pgp_encrypted = 1 THEN 1 ELSE 0 END) as is_encrypted
		FROM messages m
		JOIN messages_fts fts ON m.rowid = fts.rowid
		WHERE m.folder_id = ? AND messages_fts MATCH ?
		GROUP BY COALESCE(m.thread_id, m.id)` +
		filterHavingClause(filter, "m.") + `
		ORDER BY latest_date DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.Query(searchQuery, folderID, ftsQuery, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search conversations: %w", err)
	}
	defer rows.Close()

	var results []*ConversationSearchResult
	for rows.Next() {
		c := &ConversationSearchResult{}
		var latestDateStr sql.NullString
		var snippet sql.NullString
		var fromName sql.NullString
		var messageIDsStr sql.NullString

		err := rows.Scan(
			&c.ThreadID,
			&c.Subject,
			&snippet,
			&fromName,
			&c.MessageCount,
			&c.UnreadCount,
			&c.HasAttachments,
			&c.IsStarred,
			&latestDateStr,
			&messageIDsStr,
			&c.IsEncrypted,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan search result: %w", err)
		}

		if snippet.Valid {
			c.Snippet = snippet.String
		}
		if latestDateStr.Valid && latestDateStr.String != "" {
			c.LatestDate = parseTimeString(latestDateStr.String)
		}
		if messageIDsStr.Valid && messageIDsStr.String != "" {
			c.MessageIDs = strings.Split(messageIDsStr.String, ",")
		}

		// Set folder info
		c.FolderName = folderName
		c.FolderType = folderType

		// Apply highlighting to displayable fields
		c.HighlightedSubject = highlightMatches(c.Subject, query)
		c.HighlightedSnippet = highlightMatches(c.Snippet, query)
		if fromName.Valid {
			c.HighlightedFromName = highlightMatches(fromName.String, query)
		}

		// Get participants
		participants, _ := s.getConversationParticipants(c.ThreadID, folderID)
		c.Participants = participants

		results = append(results, c)
	}

	return results, totalCount, nil
}

// SearchConversationsUnifiedInbox searches across all inbox folders for all accounts
func (s *Store) SearchConversationsUnifiedInbox(query string, offset, limit int, filter string) ([]*ConversationSearchResult, int, error) {
	if query == "" {
		return nil, 0, nil
	}

	ftsQuery := prepareFTSQuery(query)

	// Count total results across all inbox folders
	countQuery := `
		SELECT COUNT(DISTINCT COALESCE(m.thread_id, m.id) || '-' || a.id)
		FROM messages m
		JOIN messages_fts fts ON m.rowid = fts.rowid
		INNER JOIN folders f ON m.folder_id = f.id AND f.folder_type = 'inbox'
		INNER JOIN accounts a ON f.account_id = a.id AND a.enabled = 1
		WHERE messages_fts MATCH ?
	` + filterWhereClause(filter, "m.")
	var totalCount int
	err := s.db.QueryRow(countQuery, ftsQuery).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count unified search results: %w", err)
	}

	if totalCount == 0 {
		return nil, 0, nil
	}

	// Search across all inbox folders with account info
	searchQuery := `
		SELECT 
			COALESCE(m.thread_id, m.id) as conv_thread_id,
			MIN(m.subject) as subject,
			MAX(m.snippet) as snippet,
			MIN(m.from_name) as from_name,
			COUNT(*) as message_count,
			SUM(CASE WHEN m.is_read = 0 THEN 1 ELSE 0 END) as unread_count,
			MAX(CASE WHEN m.has_attachments = 1 THEN 1 ELSE 0 END) as has_attachments,
			MAX(CASE WHEN m.is_starred = 1 THEN 1 ELSE 0 END) as is_starred,
			MAX(m.date) as latest_date,
			GROUP_CONCAT(m.id) as message_ids,
			MAX(CASE WHEN m.smime_encrypted = 1 OR m.pgp_encrypted = 1 THEN 1 ELSE 0 END) as is_encrypted,
			a.id as account_id,
			a.name as account_name,
			a.color as account_color,
			f.id as folder_id,
			f.name as folder_name,
			f.folder_type as folder_type
		FROM messages m
		JOIN messages_fts fts ON m.rowid = fts.rowid
		INNER JOIN folders f ON m.folder_id = f.id AND f.folder_type = 'inbox'
		INNER JOIN accounts a ON f.account_id = a.id AND a.enabled = 1
		WHERE messages_fts MATCH ?
		GROUP BY COALESCE(m.thread_id, m.id), a.id` +
		filterHavingClause(filter, "m.") + `
		ORDER BY latest_date DESC
		LIMIT ? OFFSET ?
	`

	rows, err := s.db.Query(searchQuery, ftsQuery, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search unified inbox: %w", err)
	}
	defer rows.Close()

	var results []*ConversationSearchResult
	for rows.Next() {
		c := &ConversationSearchResult{}
		var latestDateStr sql.NullString
		var snippet sql.NullString
		var fromName sql.NullString
		var messageIDsStr sql.NullString

		err := rows.Scan(
			&c.ThreadID,
			&c.Subject,
			&snippet,
			&fromName,
			&c.MessageCount,
			&c.UnreadCount,
			&c.HasAttachments,
			&c.IsStarred,
			&latestDateStr,
			&messageIDsStr,
			&c.IsEncrypted,
			&c.AccountID,
			&c.AccountName,
			&c.AccountColor,
			&c.FolderID,
			&c.FolderName,
			&c.FolderType,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan unified search result: %w", err)
		}

		if snippet.Valid {
			c.Snippet = snippet.String
		}
		if latestDateStr.Valid && latestDateStr.String != "" {
			c.LatestDate = parseTimeString(latestDateStr.String)
		}
		if messageIDsStr.Valid && messageIDsStr.String != "" {
			c.MessageIDs = strings.Split(messageIDsStr.String, ",")
		}

		// Apply highlighting
		c.HighlightedSubject = highlightMatches(c.Subject, query)
		c.HighlightedSnippet = highlightMatches(c.Snippet, query)
		if fromName.Valid {
			c.HighlightedFromName = highlightMatches(fromName.String, query)
		}

		// Get participants
		participants, _ := s.getConversationParticipantsUnified(c.ThreadID, c.AccountID)
		c.Participants = participants

		results = append(results, c)
	}

	return results, totalCount, nil
}

// prepareFTSQuery prepares a user query for FTS5
// Handles special characters and adds prefix matching for better UX
func prepareFTSQuery(query string) string {
	// Trim whitespace
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Split into words
	words := strings.Fields(query)
	var processedWords []string

	for _, word := range words {
		// Escape special FTS5 characters
		// FTS5 special chars: " ' ( ) * : ^
		escaped := word
		escaped = strings.ReplaceAll(escaped, "\"", "\"\"")

		// Add prefix matching (word*) for partial matches
		// But only if the word doesn't already end with *
		if !strings.HasSuffix(escaped, "*") && len(escaped) > 0 {
			escaped = "\"" + escaped + "\"*"
		}

		processedWords = append(processedWords, escaped)
	}

	// Join with spaces - FTS5 will AND them together by default
	return strings.Join(processedWords, " ")
}

// highlightMatches wraps matching terms in <mark> tags for highlighting
// The text is HTML-escaped to prevent XSS
func highlightMatches(text, query string) string {
	if text == "" || query == "" {
		return html.EscapeString(text)
	}

	// First, escape the text to prevent XSS
	escapedText := html.EscapeString(text)

	// Get individual search terms
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return escapedText
	}

	// Build a regex pattern that matches any of the search terms
	// Use word boundaries for better matching
	var patterns []string
	for _, term := range terms {
		// Escape regex special characters in the term
		escaped := regexp.QuoteMeta(term)
		patterns = append(patterns, escaped)
	}

	// Create a case-insensitive pattern
	pattern := "(?i)(" + strings.Join(patterns, "|") + ")"
	re, err := regexp.Compile(pattern)
	if err != nil {
		return escapedText
	}

	// Replace matches with highlighted version
	highlighted := re.ReplaceAllStringFunc(escapedText, func(match string) string {
		return "<mark>" + match + "</mark>"
	})

	return highlighted
}

// isWordChar returns true if the rune is a word character
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
