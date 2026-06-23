// Package imap provides IMAP client functionality for Aerion
package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// deadlineConn wraps a net.Conn to automatically set read/write deadlines
// before each operation. This prevents indefinite blocking on slow or dead
// connections that go-imap v2 doesn't handle with built-in timeouts.
type deadlineConn struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
}

// Read sets a read deadline before reading, preventing indefinite blocking
func (c *deadlineConn) Read(b []byte) (int, error) {
	if c.readTimeout > 0 {
		if err := c.Conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(b)
}

// Write sets a write deadline before writing, preventing indefinite blocking
func (c *deadlineConn) Write(b []byte) (int, error) {
	if c.writeTimeout > 0 {
		if err := c.Conn.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}

// SecurityType represents the connection security method
type SecurityType string

const (
	SecurityNone     SecurityType = "none"
	SecurityTLS      SecurityType = "tls"
	SecurityStartTLS SecurityType = "starttls"
)

// ClientConfig holds the configuration for connecting to an IMAP server
type ClientConfig struct {
	Host     string
	Port     int
	Security SecurityType
	Username string
	Password string

	// OAuth2 authentication
	AuthType    AuthType // "password" or "oauth2" (defaults to "password")
	AccessToken string   // OAuth2 access token (when AuthType is "oauth2")

	// Timeouts
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration

	// TLS config (optional, used for certificate TOFU verification)
	TLSConfig *tls.Config
}

// DefaultConfig returns a ClientConfig with sensible defaults
func DefaultConfig() ClientConfig {
	return ClientConfig{
		Port:           993,
		Security:       SecurityTLS,
		ConnectTimeout: 30 * time.Second,
		ReadTimeout:    3 * time.Minute, // Increased for large body fetches (was 30s)
		WriteTimeout:   30 * time.Second,
	}
}

// Client wraps the go-imap client with additional functionality
type Client struct {
	config ClientConfig
	client *imapclient.Client
	caps   imap.CapSet
	log    zerolog.Logger
}

// NewClient creates a new IMAP client but does not connect
func NewClient(config ClientConfig) *Client {
	return &Client{
		config: config,
		log:    logging.WithComponent("imap"),
	}
}

// Connect establishes a connection to the IMAP server and logs in
func (c *Client) Connect() error {
	addr := net.JoinHostPort(c.config.Host, strconv.Itoa(c.config.Port))

	c.log.Debug().
		Str("host", c.config.Host).
		Int("port", c.config.Port).
		Str("security", string(c.config.Security)).
		Dur("readTimeout", c.config.ReadTimeout).
		Dur("writeTimeout", c.config.WriteTimeout).
		Msg("Connecting to IMAP server")

	var err error
	options := &imapclient.Options{}

	// Create a dialer with connect timeout
	dialer := &net.Dialer{
		Timeout: c.config.ConnectTimeout,
	}

	switch c.config.Security {
	case SecurityTLS:
		// Connect with TLS directly (port 993)
		// Use custom TLSConfig if provided (for certificate TOFU), otherwise default
		tlsConfig := c.config.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{ServerName: c.config.Host}
		}
		rawConn, dialErr := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if dialErr != nil {
			return fmt.Errorf("failed to connect with TLS: %w", dialErr)
		}

		// Wrap with deadline connection for read/write timeouts
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  c.config.ReadTimeout,
			writeTimeout: c.config.WriteTimeout,
		}

		c.client = imapclient.New(wrappedConn, options)

	case SecurityStartTLS:
		// Connect plain first, wrap with deadline, then upgrade to TLS (port 143)
		rawConn, dialErr := dialer.Dial("tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("failed to connect: %w", dialErr)
		}

		// Wrap with deadline connection for read/write timeouts
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  c.config.ReadTimeout,
			writeTimeout: c.config.WriteTimeout,
		}

		// Use custom TLSConfig if provided (for certificate TOFU)
		if c.config.TLSConfig != nil {
			options.TLSConfig = c.config.TLSConfig
		}

		c.client, err = imapclient.NewStartTLS(wrappedConn, options)
		if err != nil {
			return fmt.Errorf("failed to connect with STARTTLS: %w", err)
		}

	case SecurityNone:
		// Plain connection (not recommended)
		rawConn, dialErr := dialer.Dial("tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("failed to connect: %w", dialErr)
		}

		// Wrap with deadline connection for read/write timeouts
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  c.config.ReadTimeout,
			writeTimeout: c.config.WriteTimeout,
		}

		c.client = imapclient.New(wrappedConn, options)
	}

	// Wait for server greeting
	if err := c.client.WaitGreeting(); err != nil {
		c.client.Close()
		return fmt.Errorf("failed to receive greeting: %w", err)
	}

	// Store capabilities
	c.caps = c.client.Caps()

	c.log.Debug().
		Strs("caps", capsToStrings(c.caps)).
		Msg("Server capabilities")

	c.log.Info().
		Str("host", c.config.Host).
		Dur("readTimeout", c.config.ReadTimeout).
		Msg("Connected to IMAP server with timeout protection")

	return nil
}

// capsToStrings converts CapSet to string slice for logging
func capsToStrings(caps imap.CapSet) []string {
	var result []string
	for cap := range caps {
		result = append(result, string(cap))
	}
	return result
}

// Login authenticates with the IMAP server
func (c *Client) Login() error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	// Determine auth type (default to password)
	authType := c.config.AuthType
	if authType == "" {
		authType = AuthTypePassword
	}

	c.log.Debug().
		Str("username", c.config.Username).
		Str("authType", string(authType)).
		Msg("Logging in")

	var err error
	switch authType {
	case AuthTypeOAuth2:
		err = c.loginOAuth2()
	default:
		err = c.loginPassword()
	}

	if err != nil {
		return err
	}

	// Update capabilities after login (may change)
	c.caps = c.client.Caps()

	c.log.Info().
		Str("username", c.config.Username).
		Msg("Logged in successfully")

	return nil
}

// loginPassword authenticates using password (LOGIN or SASL PLAIN)
func (c *Client) loginPassword() error {
	// Use LOGIN by default — it's simpler and more compatible.
	// Only use AUTHENTICATE PLAIN if the server advertises LOGINDISABLED,
	// since a failed AUTHENTICATE can corrupt the IMAP wire state and
	// prevent a fallback LOGIN from working (seen with Proton Bridge).
	if c.caps.Has(imap.CapLoginDisabled) {
		c.log.Debug().Msg("LOGIN disabled, using AUTHENTICATE PLAIN")
		saslClient := sasl.NewPlainClient("", c.config.Username, c.config.Password)
		if err := c.client.Authenticate(saslClient); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
		return nil
	}

	c.log.Debug().Msg("Using LOGIN command")
	if err := c.client.Login(c.config.Username, c.config.Password).Wait(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	return nil
}

// loginOAuth2 authenticates using OAuth2 XOAUTH2 SASL mechanism
func (c *Client) loginOAuth2() error {
	if c.config.AccessToken == "" {
		return fmt.Errorf("OAuth2 authentication requires an access token")
	}

	// Check if server supports XOAUTH2
	// Note: Most servers advertise AUTH=XOAUTH2 or just support it
	c.log.Debug().Msg("Authenticating with XOAUTH2")

	saslClient := NewXOAuth2Client(c.config.Username, c.config.AccessToken)

	if err := c.client.Authenticate(saslClient); err != nil {
		return fmt.Errorf("XOAUTH2 authentication failed: %w", err)
	}

	return nil
}

// Close closes the connection to the IMAP server with a graceful logout
func (c *Client) Close() error {
	if c.client == nil {
		return nil
	}

	c.log.Debug().Msg("Closing IMAP connection")

	// Try to logout gracefully
	if err := c.client.Logout().Wait(); err != nil {
		c.log.Warn().Err(err).Msg("Logout failed, closing anyway")
	}

	return c.client.Close()
}

// ForceClose closes the IMAP connection without waiting for a server response.
// It sends LOGOUT (fire-and-forget) so the server can release the connection
// slot, then immediately closes the socket. Use when the connection may be
// dead (e.g., after network changes) to avoid blocking on unresponsive
// TCP sockets while still being a good citizen to the IMAP server.
func (c *Client) ForceClose() error {
	if c.client == nil {
		return nil
	}

	c.log.Debug().Msg("Force-closing IMAP connection")

	// Send LOGOUT without waiting for the BYE response.
	// On live connections the server receives it and frees the slot.
	// On dead connections the write may fail silently — either way
	// we close the socket below.
	c.client.Logout()

	return c.client.Close()
}

// Caps returns the server capabilities
func (c *Client) Caps() imap.CapSet {
	return c.caps
}

// HasCap checks if the server supports a capability
func (c *Client) HasCap(cap imap.Cap) bool {
	return c.caps.Has(cap)
}

// SupportsQResync returns true if the server supports QRESYNC
func (c *Client) SupportsQResync() bool {
	return c.caps.Has(imap.CapQResync)
}

// SupportsCondStore returns true if the server supports CONDSTORE
func (c *Client) SupportsCondStore() bool {
	return c.caps.Has(imap.CapCondStore)
}

// SupportsIdle returns true if the server supports IDLE
func (c *Client) SupportsIdle() bool {
	return c.caps.Has(imap.CapIdle)
}

// Mailbox represents an IMAP mailbox (folder)
type Mailbox struct {
	Name       string
	Delimiter  string
	Attributes []string
	Type       FolderType

	// Status info (populated by Status or Select)
	UIDValidity   uint32
	UIDNext       uint32
	Messages      uint32
	Unseen        uint32
	HighestModSeq uint64
}

// FolderType represents the type of folder
type FolderType string

const (
	FolderTypeInbox   FolderType = "inbox"
	FolderTypeSent    FolderType = "sent"
	FolderTypeDrafts  FolderType = "drafts"
	FolderTypeTrash   FolderType = "trash"
	FolderTypeSpam    FolderType = "spam"
	FolderTypeArchive FolderType = "archive"
	FolderTypeAll     FolderType = "all"
	FolderTypeStarred FolderType = "starred"
	FolderTypeFolder  FolderType = "folder"
)

// ListMailboxes returns a list of all mailboxes (folders)
func (c *Client) ListMailboxes() ([]*Mailbox, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	c.log.Debug().Msg("Listing mailboxes")

	// List all mailboxes
	listCmd := c.client.List("", "*", nil)

	var mailboxes []*Mailbox
	for {
		mbox := listCmd.Next()
		if mbox == nil {
			break
		}

		mb := &Mailbox{
			Name:       mbox.Mailbox,
			Delimiter:  string(mbox.Delim),
			Attributes: make([]string, len(mbox.Attrs)),
		}

		for i, attr := range mbox.Attrs {
			mb.Attributes[i] = string(attr)
		}

		// Determine folder type from attributes
		mb.Type = determineFolderType(mbox.Mailbox, mbox.Attrs)

		c.log.Debug().
			Str("mailbox", mbox.Mailbox).
			Strs("attrs", mb.Attributes).
			Str("detectedType", string(mb.Type)).
			Msg("Detected folder type")

		mailboxes = append(mailboxes, mb)
	}

	if err := listCmd.Close(); err != nil {
		return nil, fmt.Errorf("failed to list mailboxes: %w", err)
	}

	// Dedup special types: if a type was claimed via SPECIAL-USE attribute,
	// demote any name-only matches to plain folders. This prevents stale
	// "Sent" or "sent-mail" folders (created by other clients) from shadowing
	// the real provider folder (e.g. [Gmail]/Sent Mail).
	attrTypes := make(map[FolderType]bool)
	for _, mb := range mailboxes {
		if mb.Type != FolderTypeFolder && mb.Type != FolderTypeInbox && hasSpecialUseAttr(mb.Attributes) {
			attrTypes[mb.Type] = true
		}
	}
	if len(attrTypes) > 0 {
		for _, mb := range mailboxes {
			if mb.Type != FolderTypeFolder && mb.Type != FolderTypeInbox && attrTypes[mb.Type] && !hasSpecialUseAttr(mb.Attributes) {
				c.log.Debug().
					Str("mailbox", mb.Name).
					Str("type", string(mb.Type)).
					Msg("Demoting name-matched folder (SPECIAL-USE folder exists for this type)")
				mb.Type = FolderTypeFolder
			}
		}
	}

	c.log.Debug().Int("count", len(mailboxes)).Msg("Listed mailboxes")

	return mailboxes, nil
}

// Subscribe sends an IMAP SUBSCRIBE command for the given mailbox.
func (c *Client) Subscribe(mailbox string) error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}
	if err := c.client.Subscribe(mailbox).Wait(); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", mailbox, err)
	}
	c.log.Debug().Str("mailbox", mailbox).Msg("Subscribed to mailbox")
	return nil
}

// Unsubscribe sends an IMAP UNSUBSCRIBE command for the given mailbox.
func (c *Client) Unsubscribe(mailbox string) error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}
	if err := c.client.Unsubscribe(mailbox).Wait(); err != nil {
		return fmt.Errorf("failed to unsubscribe from %s: %w", mailbox, err)
	}
	c.log.Debug().Str("mailbox", mailbox).Msg("Unsubscribed from mailbox")
	return nil
}

// ListSubscribedMailboxes returns only the subscribed mailboxes.
func (c *Client) ListSubscribedMailboxes() (map[string]bool, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	c.log.Debug().Msg("Listing subscribed mailboxes")

	listCmd := c.client.List("", "*", &imap.ListOptions{SelectSubscribed: true})

	subscribed := make(map[string]bool)
	for {
		mbox := listCmd.Next()
		if mbox == nil {
			break
		}
		subscribed[mbox.Mailbox] = true
	}

	if err := listCmd.Close(); err != nil {
		return nil, fmt.Errorf("failed to list subscribed mailboxes: %w", err)
	}

	c.log.Debug().Int("count", len(subscribed)).Msg("Listed subscribed mailboxes")
	return subscribed, nil
}

// determineFolderType determines the folder type from name and attributes
func determineFolderType(name string, attrs []imap.MailboxAttr) FolderType {
	// Check attributes first (RFC 6154 special-use)
	for _, attr := range attrs {
		switch attr {
		case imap.MailboxAttrAll:
			return FolderTypeAll
		case imap.MailboxAttrArchive:
			return FolderTypeArchive
		case imap.MailboxAttrDrafts:
			return FolderTypeDrafts
		case imap.MailboxAttrJunk:
			return FolderTypeSpam
		case imap.MailboxAttrSent:
			return FolderTypeSent
		case imap.MailboxAttrTrash:
			return FolderTypeTrash
		case imap.MailboxAttrFlagged:
			return FolderTypeStarred
		}
	}

	// Fall back to name matching
	switch {
	case name == "INBOX":
		return FolderTypeInbox
	case containsIgnoreCase(name, "sent"):
		return FolderTypeSent
	case containsIgnoreCase(name, "draft"):
		return FolderTypeDrafts
	case containsIgnoreCase(name, "trash") || containsIgnoreCase(name, "deleted") || containsIgnoreCase(name, "bin"):
		return FolderTypeTrash
	case containsIgnoreCase(name, "spam") || containsIgnoreCase(name, "junk"):
		return FolderTypeSpam
	case containsIgnoreCase(name, "archive"):
		return FolderTypeArchive
	case containsIgnoreCase(name, "all mail"):
		return FolderTypeAll
	case containsIgnoreCase(name, "starred") || containsIgnoreCase(name, "flagged"):
		return FolderTypeStarred
	}

	return FolderTypeFolder
}

func containsIgnoreCase(s, substr string) bool {
	// Simple case-insensitive contains
	for i := 0; i+len(substr) <= len(s); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c1 := s[i+j]
			c2 := substr[j]
			// Convert to lowercase for comparison
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// hasSpecialUseAttr checks if a mailbox has any RFC 6154 SPECIAL-USE attribute
func hasSpecialUseAttr(attrs []string) bool {
	for _, attr := range attrs {
		switch imap.MailboxAttr(attr) {
		case imap.MailboxAttrAll, imap.MailboxAttrArchive, imap.MailboxAttrDrafts,
			imap.MailboxAttrJunk, imap.MailboxAttrSent, imap.MailboxAttrTrash,
			imap.MailboxAttrFlagged:
			return true
		}
	}
	return false
}

// SelectMailbox selects a mailbox and returns its status.
// Uses a goroutine to allow context cancellation since Wait() blocks indefinitely.
func (c *Client) SelectMailbox(ctx context.Context, name string) (*Mailbox, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	c.log.Debug().Str("mailbox", name).Msg("Selecting mailbox")

	// Enable CONDSTORE on SELECT when the server supports it. This makes the
	// server report HIGHESTMODSEQ in the SELECT response and lets subsequent
	// FETCH (CHANGEDSINCE) calls return only the UIDs whose flags changed
	// since the last sync — instead of fetching flags for every UID every
	// cycle (which scales O(mailbox-size) per sync).
	// Per RFC 7162 §3.1, enabling CONDSTORE without a follow-up CHANGEDSINCE/
	// UNCHANGEDSINCE is a no-op on the wire — so this is safe for every
	// caller of SelectMailbox, not just the flag-sync path.
	var selectOpts *imap.SelectOptions
	if c.SupportsCondStore() {
		selectOpts = &imap.SelectOptions{CondStore: true}
	}

	// Run Wait() in a goroutine to allow context cancellation
	type selectResult struct {
		data *imap.SelectData
		err  error
	}
	resultCh := make(chan selectResult, 1)
	go func() {
		data, err := c.client.Select(name, selectOpts).Wait()
		resultCh <- selectResult{data, err}
	}()

	// Wait for either result or context cancellation
	select {
	case <-ctx.Done():
		c.log.Debug().Str("mailbox", name).Msg("Select cancelled by context")
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return nil, fmt.Errorf("failed to select mailbox: %w", result.err)
		}

		mb := &Mailbox{
			Name:        name,
			UIDValidity: result.data.UIDValidity,
			UIDNext:     uint32(result.data.UIDNext),
			Messages:    result.data.NumMessages,
		}

		// Get highest mod seq if available
		if result.data.HighestModSeq != 0 {
			mb.HighestModSeq = result.data.HighestModSeq
		}

		c.log.Debug().
			Str("mailbox", name).
			Uint32("messages", result.data.NumMessages).
			Uint32("uidValidity", result.data.UIDValidity).
			Msg("Selected mailbox")

		return mb, nil
	}
}

// GetMailboxStatus returns the status of a mailbox without selecting it.
// Uses a goroutine to allow context cancellation since Wait() blocks indefinitely.
func (c *Client) GetMailboxStatus(ctx context.Context, name string) (*Mailbox, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}

	options := &imap.StatusOptions{
		NumMessages: true,
		UIDNext:     true,
		UIDValidity: true,
		NumUnseen:   true,
	}

	// Add HIGHESTMODSEQ if server supports CONDSTORE
	if c.SupportsCondStore() {
		options.HighestModSeq = true
	}

	// Run Wait() in a goroutine to allow context cancellation
	type statusResult struct {
		data *imap.StatusData
		err  error
	}
	resultCh := make(chan statusResult, 1)
	go func() {
		data, err := c.client.Status(name, options).Wait()
		resultCh <- statusResult{data, err}
	}()

	// Wait for either result or context cancellation
	select {
	case <-ctx.Done():
		c.log.Debug().Str("mailbox", name).Msg("Status cancelled by context")
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return nil, fmt.Errorf("failed to get mailbox status: %w", result.err)
		}

		mb := &Mailbox{
			Name: name,
		}

		// Handle pointer fields
		if result.data.UIDValidity != 0 {
			mb.UIDValidity = result.data.UIDValidity
		}
		if result.data.UIDNext != 0 {
			mb.UIDNext = uint32(result.data.UIDNext)
		}
		if result.data.NumMessages != nil {
			mb.Messages = *result.data.NumMessages
		}
		if result.data.NumUnseen != nil {
			mb.Unseen = *result.data.NumUnseen
		}
		if result.data.HighestModSeq != 0 {
			mb.HighestModSeq = result.data.HighestModSeq
		}

		return mb, nil
	}
}

// RawClient returns the underlying imapclient.Client
// Use with caution - mainly for advanced operations
func (c *Client) RawClient() *imapclient.Client {
	return c.client
}

// AppendMessage appends a message to a mailbox and returns the assigned UID
func (c *Client) AppendMessage(mailbox string, flags []imap.Flag, date time.Time, msg []byte) (imap.UID, error) {
	if c.client == nil {
		return 0, fmt.Errorf("not connected")
	}

	c.log.Debug().
		Str("mailbox", mailbox).
		Int("size", len(msg)).
		Strs("flags", flagsToStrings(flags)).
		Msg("Appending message")

	options := &imap.AppendOptions{
		Flags: flags,
	}
	if !date.IsZero() {
		options.Time = date
	}

	appendCmd := c.client.Append(mailbox, int64(len(msg)), options)

	// Write the message data
	if _, err := appendCmd.Write(msg); err != nil {
		return 0, fmt.Errorf("failed to write message data: %w", err)
	}

	if err := appendCmd.Close(); err != nil {
		return 0, fmt.Errorf("failed to close append command: %w", err)
	}

	// Wait for the response
	data, err := appendCmd.Wait()
	if err != nil {
		return 0, fmt.Errorf("failed to append message: %w", err)
	}

	c.log.Debug().
		Str("mailbox", mailbox).
		Uint32("uid", uint32(data.UID)).
		Msg("Message appended successfully")

	return data.UID, nil
}

// DeleteMessageByUID marks a message as deleted and expunges it
// The mailbox must already be selected before calling this method
func (c *Client) DeleteMessageByUID(uid imap.UID) error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	c.log.Debug().
		Uint32("uid", uint32(uid)).
		Msg("Deleting message by UID")

	// Create a UID set with just this UID
	uidSet := imap.UIDSet{}
	uidSet.AddNum(uid)

	// Store the \Deleted flag
	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}

	storeCmd := c.client.Store(uidSet, &storeFlags, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("failed to mark message as deleted: %w", err)
	}

	// Expunge to permanently remove the message
	// Use UID EXPUNGE if server supports UIDPLUS (RFC 4315) for safer deletion
	// UID EXPUNGE only removes the specific UIDs, not all \Deleted messages
	if c.caps.Has(imap.CapUIDPlus) {
		expungeCmd := c.client.UIDExpunge(uidSet)
		if err := expungeCmd.Close(); err != nil {
			return fmt.Errorf("failed to expunge message: %w", err)
		}
		c.log.Debug().
			Uint32("uid", uint32(uid)).
			Msg("Message deleted successfully (using UID EXPUNGE)")
	} else {
		// Fall back to regular EXPUNGE (affects all \Deleted messages)
		expungeCmd := c.client.Expunge()
		if err := expungeCmd.Close(); err != nil {
			return fmt.Errorf("failed to expunge message: %w", err)
		}
		c.log.Debug().
			Uint32("uid", uint32(uid)).
			Msg("Message deleted successfully (using EXPUNGE)")
	}

	return nil
}

// flagsToStrings converts IMAP flags to string slice for logging
func flagsToStrings(flags []imap.Flag) []string {
	result := make([]string, len(flags))
	for i, f := range flags {
		result[i] = string(f)
	}
	return result
}

// AddMessageFlags adds flags to messages by UID
// The mailbox must already be selected before calling this method
func (c *Client) AddMessageFlags(uids []imap.UID, flags []imap.Flag) error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}

	c.log.Debug().
		Interface("uids", uidsToUint32s(uids)).
		Strs("flags", flagsToStrings(flags)).
		Msg("Adding flags to messages")

	uidSet := imap.UIDSet{}
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  flags,
		Silent: true,
	}

	storeCmd := c.client.Store(uidSet, &storeFlags, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("failed to add flags: %w", err)
	}

	c.log.Debug().
		Int("count", len(uids)).
		Msg("Flags added successfully")

	return nil
}

// RemoveMessageFlags removes flags from messages by UID
// The mailbox must already be selected before calling this method
func (c *Client) RemoveMessageFlags(uids []imap.UID, flags []imap.Flag) error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}

	c.log.Debug().
		Interface("uids", uidsToUint32s(uids)).
		Strs("flags", flagsToStrings(flags)).
		Msg("Removing flags from messages")

	uidSet := imap.UIDSet{}
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsDel,
		Flags:  flags,
		Silent: true,
	}

	storeCmd := c.client.Store(uidSet, &storeFlags, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("failed to remove flags: %w", err)
	}

	c.log.Debug().
		Int("count", len(uids)).
		Msg("Flags removed successfully")

	return nil
}

// CopyMessages copies messages to destination mailbox by UID
// The source mailbox must already be selected before calling this method
// Returns the new UIDs in the destination mailbox (if server supports UIDPLUS)
func (c *Client) CopyMessages(uids []imap.UID, destMailbox string) ([]imap.UID, error) {
	if c.client == nil {
		return nil, fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil, nil
	}

	c.log.Debug().
		Interface("uids", uidsToUint32s(uids)).
		Str("destMailbox", destMailbox).
		Msg("Copying messages")

	uidSet := imap.UIDSet{}
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	copyCmd := c.client.Copy(uidSet, destMailbox)
	copyData, err := copyCmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("failed to copy messages: %w", err)
	}

	// Extract destination UIDs if available (UIDPLUS extension)
	var destUIDs []imap.UID
	if copyData != nil && copyData.DestUIDs != nil {
		// The DestUIDs is a UIDSet - for now we just log that it's available
		// Full extraction would require iterating the UIDSet ranges
		c.log.Debug().Msg("Messages copied with UIDPLUS, destination UIDs available")
	}

	c.log.Debug().
		Int("count", len(uids)).
		Str("destMailbox", destMailbox).
		Msg("Messages copied successfully")

	return destUIDs, nil
}

// DeleteMessagesByUID marks multiple messages as deleted and expunges them
// The mailbox must already be selected before calling this method
func (c *Client) DeleteMessagesByUID(uids []imap.UID) error {
	if c.client == nil {
		return fmt.Errorf("not connected")
	}
	if len(uids) == 0 {
		return nil
	}

	c.log.Debug().
		Interface("uids", uidsToUint32s(uids)).
		Msg("Deleting messages by UID")

	uidSet := imap.UIDSet{}
	for _, uid := range uids {
		uidSet.AddNum(uid)
	}

	// Store the \Deleted flag
	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}

	storeCmd := c.client.Store(uidSet, &storeFlags, nil)
	if err := storeCmd.Close(); err != nil {
		return fmt.Errorf("failed to mark messages as deleted: %w", err)
	}

	// Expunge to permanently remove
	// Use UID EXPUNGE if server supports UIDPLUS (RFC 4315) for safer deletion
	// UID EXPUNGE only removes the specific UIDs, not all \Deleted messages
	if c.caps.Has(imap.CapUIDPlus) {
		expungeCmd := c.client.UIDExpunge(uidSet)
		if err := expungeCmd.Close(); err != nil {
			return fmt.Errorf("failed to expunge messages: %w", err)
		}
		c.log.Debug().
			Int("count", len(uids)).
			Msg("Messages deleted successfully (using UID EXPUNGE)")
	} else {
		// Fall back to regular EXPUNGE (affects all \Deleted messages)
		expungeCmd := c.client.Expunge()
		if err := expungeCmd.Close(); err != nil {
			return fmt.Errorf("failed to expunge messages: %w", err)
		}
		c.log.Debug().
			Int("count", len(uids)).
			Msg("Messages deleted successfully (using EXPUNGE)")
	}

	return nil
}

// uidsToUint32s converts a slice of imap.UID to uint32 for logging
func uidsToUint32s(uids []imap.UID) []uint32 {
	result := make([]uint32, len(uids))
	for i, uid := range uids {
		result[i] = uint32(uid)
	}
	return result
}
