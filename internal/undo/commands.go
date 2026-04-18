package undo

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
	imapPkg "github.com/hkdb/aerion/internal/imap"
)

// UndoContext provides dependencies for undo operations
type UndoContext interface {
	// GetIMAPConnectionForUndo returns an IMAP client for the account
	GetIMAPConnectionForUndo(ctx context.Context, accountID string) (*imapPkg.Client, func(), error)
	// UpdateLocalFlags updates flags in local database
	UpdateLocalFlags(messageIDs []string, isRead, isStarred *bool) error
	// MoveLocalMessages moves messages in local database
	MoveLocalMessages(messageIDs []string, folderID string) error
	// DeleteLocalMessages deletes messages from local database
	DeleteLocalMessages(messageIDs []string) error
	// FindLocalMessageIDs finds current local DB message IDs by RFC822 Message-ID and folder
	FindLocalMessageIDs(accountID, folderID string, rfc822MessageIDs []string) ([]string, error)
	// MoveMessagesToFolder moves messages using the full move pipeline (IMAP + local DB)
	MoveMessagesToFolder(messageIDs []string, destFolderID string) error
}

// FlagChangeCommand handles read/star flag changes
type FlagChangeCommand struct {
	BaseCommand
	ctx           context.Context
	undoCtx       UndoContext
	accountID     string
	folderPath    string
	messageIDs    []string
	uids          []uint32
	flagType      string // "read" or "starred"
	previousState bool   // What was the state before
}

// NewFlagChangeCommand creates a new FlagChangeCommand
func NewFlagChangeCommand(
	ctx context.Context,
	undoCtx UndoContext,
	accountID, folderPath string,
	messageIDs []string,
	uids []uint32,
	flagType string,
	previousState bool,
	description string,
) *FlagChangeCommand {
	return &FlagChangeCommand{
		BaseCommand:   NewBaseCommand(description),
		ctx:           ctx,
		undoCtx:       undoCtx,
		accountID:     accountID,
		folderPath:    folderPath,
		messageIDs:    messageIDs,
		uids:          uids,
		flagType:      flagType,
		previousState: previousState,
	}
}

// Execute performs the action (already done at creation time)
func (c *FlagChangeCommand) Execute() error { return nil }

// Undo reverses the flag change
func (c *FlagChangeCommand) Undo() error {
	// Get IMAP connection
	client, release, err := c.undoCtx.GetIMAPConnectionForUndo(c.ctx, c.accountID)
	if err != nil {
		return fmt.Errorf("failed to get IMAP connection: %w", err)
	}
	defer release()

	// Select mailbox
	if _, err := client.SelectMailbox(c.ctx, c.folderPath); err != nil {
		return fmt.Errorf("failed to select mailbox: %w", err)
	}

	// Convert UIDs
	imapUIDs := make([]imap.UID, len(c.uids))
	for i, uid := range c.uids {
		imapUIDs[i] = imap.UID(uid)
	}

	// Determine flag
	var flag imap.Flag
	switch c.flagType {
	case "read":
		flag = imap.FlagSeen
	case "starred":
		flag = imap.FlagFlagged
	default:
		return fmt.Errorf("unknown flag type: %s", c.flagType)
	}

	// Restore previous state on IMAP
	if c.previousState {
		if err := client.AddMessageFlags(imapUIDs, []imap.Flag{flag}); err != nil {
			return fmt.Errorf("failed to add flags: %w", err)
		}
	} else {
		if err := client.RemoveMessageFlags(imapUIDs, []imap.Flag{flag}); err != nil {
			return fmt.Errorf("failed to remove flags: %w", err)
		}
	}

	// Update local database
	var isRead, isStarred *bool
	switch c.flagType {
	case "read":
		isRead = &c.previousState
	case "starred":
		isStarred = &c.previousState
	}
	if err := c.undoCtx.UpdateLocalFlags(c.messageIDs, isRead, isStarred); err != nil {
		return fmt.Errorf("failed to update local flags: %w", err)
	}

	return nil
}

// MoveCommand handles moving messages between folders
type MoveCommand struct {
	BaseCommand
	undoCtx          UndoContext
	accountID        string
	rfc822MessageIDs []string // RFC822 Message-ID headers for reliable lookup
	sourceFolderID   string
	destFolderID     string
}

// NewMoveCommand creates a new MoveCommand
func NewMoveCommand(
	undoCtx UndoContext,
	accountID string,
	rfc822MessageIDs []string,
	sourceFolderID string,
	destFolderID string,
	description string,
) *MoveCommand {
	return &MoveCommand{
		BaseCommand:      NewBaseCommand(description),
		undoCtx:          undoCtx,
		accountID:        accountID,
		rfc822MessageIDs: rfc822MessageIDs,
		sourceFolderID:   sourceFolderID,
		destFolderID:     destFolderID,
	}
}

// Execute performs the action (already done at creation time)
func (c *MoveCommand) Execute() error { return nil }

// Undo reverses the move by finding current messages in the destination folder
// and moving them back using the standard move pipeline.
func (c *MoveCommand) Undo() error {
	// Find current local message IDs by RFC822 Message-ID in the destination folder
	localMsgIDs, err := c.undoCtx.FindLocalMessageIDs(c.accountID, c.destFolderID, c.rfc822MessageIDs)
	if err != nil {
		return fmt.Errorf("failed to find messages: %w", err)
	}
	if len(localMsgIDs) == 0 {
		return fmt.Errorf("messages not found in destination folder")
	}

	// Reuse the full move pipeline (IMAP + local DB + events)
	return c.undoCtx.MoveMessagesToFolder(localMsgIDs, c.sourceFolderID)
}
