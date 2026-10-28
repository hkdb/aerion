package folder

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hkdb/aerion/internal/database"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// Store provides folder persistence operations
type Store struct {
	db  *database.DB
	log zerolog.Logger
}

// NewStore creates a new folder store
func NewStore(db *database.DB) *Store {
	return &Store{
		db:  db,
		log: logging.WithComponent("folder-store"),
	}
}

// List returns all folders for an account
func (s *Store) List(accountID string) ([]*Folder, error) {
	query := `
		SELECT id, account_id, name, path, folder_type, parent_id,
		       uid_validity, uid_next, highest_mod_seq,
		       total_count, unread_count, last_sync, subscribed
		FROM folders
		WHERE account_id = ?
		ORDER BY name
	`

	rows, err := s.db.Query(query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query folders: %w", err)
	}
	defer rows.Close()

	var folders []*Folder
	for rows.Next() {
		f := &Folder{}
		var parentID sql.NullString
		var lastSync sql.NullTime
		var uidValidity, uidNext sql.NullInt64
		var highestModSeq sql.NullInt64

		err := rows.Scan(
			&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Type, &parentID,
			&uidValidity, &uidNext, &highestModSeq,
			&f.TotalCount, &f.UnreadCount, &lastSync, &f.Subscribed,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan folder: %w", err)
		}

		if parentID.Valid {
			f.ParentID = parentID.String
		}
		if lastSync.Valid {
			f.LastSync = &lastSync.Time
		}
		if uidValidity.Valid {
			f.UIDValidity = uint32(uidValidity.Int64)
		}
		if uidNext.Valid {
			f.UIDNext = uint32(uidNext.Int64)
		}
		if highestModSeq.Valid {
			f.HighestModSeq = uint64(highestModSeq.Int64)
		}

		folders = append(folders, f)
	}

	return folders, nil
}

// Get returns a folder by ID
func (s *Store) Get(id string) (*Folder, error) {
	query := `
		SELECT id, account_id, name, path, folder_type, parent_id,
		       uid_validity, uid_next, highest_mod_seq,
		       total_count, unread_count, last_sync, subscribed
		FROM folders
		WHERE id = ?
	`

	f := &Folder{}
	var parentID sql.NullString
	var lastSync sql.NullTime
	var uidValidity, uidNext sql.NullInt64
	var highestModSeq sql.NullInt64

	err := s.db.QueryRow(query, id).Scan(
		&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Type, &parentID,
		&uidValidity, &uidNext, &highestModSeq,
		&f.TotalCount, &f.UnreadCount, &lastSync, &f.Subscribed,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	if parentID.Valid {
		f.ParentID = parentID.String
	}
	if lastSync.Valid {
		f.LastSync = &lastSync.Time
	}
	if uidValidity.Valid {
		f.UIDValidity = uint32(uidValidity.Int64)
	}
	if uidNext.Valid {
		f.UIDNext = uint32(uidNext.Int64)
	}
	if highestModSeq.Valid {
		f.HighestModSeq = uint64(highestModSeq.Int64)
	}

	return f, nil
}

// GetByPath returns a folder by account ID and path
func (s *Store) GetByPath(accountID, path string) (*Folder, error) {
	query := `
		SELECT id, account_id, name, path, folder_type, parent_id,
		       uid_validity, uid_next, highest_mod_seq,
		       total_count, unread_count, last_sync, subscribed
		FROM folders
		WHERE account_id = ? AND path = ?
	`

	f := &Folder{}
	var parentID sql.NullString
	var lastSync sql.NullTime
	var uidValidity, uidNext sql.NullInt64
	var highestModSeq sql.NullInt64

	err := s.db.QueryRow(query, accountID, path).Scan(
		&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Type, &parentID,
		&uidValidity, &uidNext, &highestModSeq,
		&f.TotalCount, &f.UnreadCount, &lastSync, &f.Subscribed,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get folder: %w", err)
	}

	if parentID.Valid {
		f.ParentID = parentID.String
	}
	if lastSync.Valid {
		f.LastSync = &lastSync.Time
	}
	if uidValidity.Valid {
		f.UIDValidity = uint32(uidValidity.Int64)
	}
	if uidNext.Valid {
		f.UIDNext = uint32(uidNext.Int64)
	}
	if highestModSeq.Valid {
		f.HighestModSeq = uint64(highestModSeq.Int64)
	}

	return f, nil
}

// Create creates a new folder
func (s *Store) Create(f *Folder) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}

	query := `
		INSERT INTO folders (id, account_id, name, path, folder_type, parent_id,
		                     uid_validity, uid_next, highest_mod_seq,
		                     total_count, unread_count, last_sync, subscribed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var parentID interface{}
	if f.ParentID != "" {
		parentID = f.ParentID
	}

	var lastSync interface{}
	if f.LastSync != nil {
		lastSync = f.LastSync
	}

	_, err := s.db.Exec(query,
		f.ID, f.AccountID, f.Name, f.Path, f.Type, parentID,
		f.UIDValidity, f.UIDNext, f.HighestModSeq,
		f.TotalCount, f.UnreadCount, lastSync, f.Subscribed,
	)
	if err != nil {
		return fmt.Errorf("failed to create folder: %w", err)
	}

	s.log.Debug().
		Str("id", f.ID).
		Str("path", f.Path).
		Msg("Created folder")

	return nil
}

// Update updates an existing folder
func (s *Store) Update(f *Folder) error {
	query := `
		UPDATE folders SET
			name = ?,
			folder_type = ?,
			parent_id = ?,
			uid_validity = ?,
			uid_next = ?,
			highest_mod_seq = ?,
			total_count = ?,
			unread_count = ?,
			last_sync = ?,
			subscribed = ?
		WHERE id = ?
	`

	var parentID interface{}
	if f.ParentID != "" {
		parentID = f.ParentID
	}

	var lastSync interface{}
	if f.LastSync != nil {
		lastSync = f.LastSync
	}

	_, err := s.db.Exec(query,
		f.Name, f.Type, parentID,
		f.UIDValidity, f.UIDNext, f.HighestModSeq,
		f.TotalCount, f.UnreadCount, lastSync, f.Subscribed,
		f.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update folder: %w", err)
	}

	return nil
}

// UpdateSyncState updates only the sync-related fields
func (s *Store) UpdateSyncState(id string, uidValidity, uidNext uint32, highestModSeq uint64, totalCount, unreadCount int) error {
	now := time.Now()
	query := `
		UPDATE folders SET
			uid_validity = ?,
			uid_next = ?,
			highest_mod_seq = ?,
			total_count = ?,
			unread_count = ?,
			last_sync = ?
		WHERE id = ?
	`

	_, err := s.db.Exec(query, uidValidity, uidNext, highestModSeq, totalCount, unreadCount, now, id)
	if err != nil {
		return fmt.Errorf("failed to update sync state: %w", err)
	}

	return nil
}

// AdjustCounts applies deltas to a folder's counts and returns the updated
// folder. The stored counts are the server's whole-mailbox numbers (STATUS),
// while local message rows only cover the sync window, so local actions must
// shift the stored value rather than recount it. Clamped at zero and applied
// in one statement so a concurrent sync write is never clobbered by a stale
// read-modify-write.
func (s *Store) AdjustCounts(id string, totalDelta, unreadDelta int) (*Folder, error) {
	query := `UPDATE folders
	          SET total_count = MAX(0, total_count + ?),
	              unread_count = MAX(0, unread_count + ?)
	          WHERE id = ?`
	if _, err := s.db.Exec(query, totalDelta, unreadDelta, id); err != nil {
		return nil, fmt.Errorf("failed to adjust counts: %w", err)
	}
	f, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("folder not found: %s", id)
	}
	return f, nil
}

// Delete deletes a folder
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM folders WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete folder: %w", err)
	}
	return nil
}

// DeleteByAccount deletes all folders for an account
func (s *Store) DeleteByAccount(accountID string) error {
	_, err := s.db.Exec("DELETE FROM folders WHERE account_id = ?", accountID)
	if err != nil {
		return fmt.Errorf("failed to delete folders: %w", err)
	}
	return nil
}

// Upsert creates or updates a folder based on account_id and path
func (s *Store) Upsert(f *Folder) error {
	existing, err := s.GetByPath(f.AccountID, f.Path)
	if err != nil {
		return err
	}

	if existing != nil {
		// Update existing
		f.ID = existing.ID
		return s.Update(f)
	}

	// Create new
	return s.Create(f)
}

// GetByType returns a folder by account ID and folder type
func (s *Store) GetByType(accountID string, folderType Type) (*Folder, error) {
	query := `
		SELECT id, account_id, name, path, folder_type, parent_id,
		       uid_validity, uid_next, highest_mod_seq,
		       total_count, unread_count, last_sync, subscribed
		FROM folders
		WHERE account_id = ? AND folder_type = ?
		LIMIT 1
	`

	f := &Folder{}
	var parentID sql.NullString
	var lastSync sql.NullTime
	var uidValidity, uidNext sql.NullInt64
	var highestModSeq sql.NullInt64

	err := s.db.QueryRow(query, accountID, folderType).Scan(
		&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Type, &parentID,
		&uidValidity, &uidNext, &highestModSeq,
		&f.TotalCount, &f.UnreadCount, &lastSync, &f.Subscribed,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get folder by type: %w", err)
	}

	if parentID.Valid {
		f.ParentID = parentID.String
	}
	if lastSync.Valid {
		f.LastSync = &lastSync.Time
	}
	if uidValidity.Valid {
		f.UIDValidity = uint32(uidValidity.Int64)
	}
	if uidNext.Valid {
		f.UIDNext = uint32(uidNext.Int64)
	}
	if highestModSeq.Valid {
		f.HighestModSeq = uint64(highestModSeq.Int64)
	}

	return f, nil
}

// ListSubscribed returns only subscribed folders for an account.
// Core folders (Inbox, Drafts, Sent) are always included regardless of subscription state.
func (s *Store) ListSubscribed(accountID string) ([]*Folder, error) {
	query := `
		SELECT id, account_id, name, path, folder_type, parent_id,
		       uid_validity, uid_next, highest_mod_seq,
		       total_count, unread_count, last_sync, subscribed
		FROM folders
		WHERE account_id = ? AND (subscribed = 1 OR folder_type IN ('inbox', 'drafts', 'sent'))
		ORDER BY name
	`

	rows, err := s.db.Query(query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query subscribed folders: %w", err)
	}
	defer rows.Close()

	var folders []*Folder
	for rows.Next() {
		f := &Folder{}
		var parentID sql.NullString
		var lastSync sql.NullTime
		var uidValidity, uidNext sql.NullInt64
		var highestModSeq sql.NullInt64

		err := rows.Scan(
			&f.ID, &f.AccountID, &f.Name, &f.Path, &f.Type, &parentID,
			&uidValidity, &uidNext, &highestModSeq,
			&f.TotalCount, &f.UnreadCount, &lastSync, &f.Subscribed,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan folder: %w", err)
		}

		if parentID.Valid {
			f.ParentID = parentID.String
		}
		if lastSync.Valid {
			f.LastSync = &lastSync.Time
		}
		if uidValidity.Valid {
			f.UIDValidity = uint32(uidValidity.Int64)
		}
		if uidNext.Valid {
			f.UIDNext = uint32(uidNext.Int64)
		}
		if highestModSeq.Valid {
			f.HighestModSeq = uint64(highestModSeq.Int64)
		}

		folders = append(folders, f)
	}

	return folders, nil
}

// UpdateSubscribed updates the IMAP subscription state for a folder.
func (s *Store) UpdateSubscribed(folderID string, subscribed bool) error {
	_, err := s.db.Exec(`UPDATE folders SET subscribed = ? WHERE id = ?`, subscribed, folderID)
	if err != nil {
		return fmt.Errorf("failed to update folder subscription: %w", err)
	}
	return nil
}
