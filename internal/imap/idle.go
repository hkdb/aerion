package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-sasl"
	"github.com/hkdb/aerion/internal/logging"
	"github.com/rs/zerolog"
)

// IdleConfig configures IDLE connections
type IdleConfig struct {
	// IdleTimeout is how long to stay in IDLE before restarting (RFC 2177 recommends < 29 min)
	IdleTimeout time.Duration

	// ReconnectBackoff is the initial backoff duration for reconnection attempts
	ReconnectBackoff time.Duration

	// MaxReconnectBackoff is the maximum backoff duration
	MaxReconnectBackoff time.Duration

	// MaxReconnectAttempts is the maximum number of reconnection attempts before giving up
	MaxReconnectAttempts int

	// EventSendTimeout is how long to wait when sending events before dropping them
	EventSendTimeout time.Duration

	// HealthCheckEnabled enables NOOP health checks before entering IDLE
	HealthCheckEnabled bool

	// ShutdownTimeout is how long to wait for graceful shutdown
	ShutdownTimeout time.Duration
}

// DefaultIdleConfig returns sensible defaults for IDLE
func DefaultIdleConfig() IdleConfig {
	return IdleConfig{
		IdleTimeout:          10 * time.Minute, // Shorter cycle for better connection health
		ReconnectBackoff:     1 * time.Second,
		MaxReconnectBackoff:  5 * time.Minute,
		MaxReconnectAttempts: 10,
		EventSendTimeout:     2 * time.Second,  // Don't block forever on event send
		HealthCheckEnabled:   true,             // Verify connection before IDLE
		ShutdownTimeout:      5 * time.Second,  // Graceful shutdown timeout
	}
}

// IdleConnection manages an IDLE connection for a single account
type IdleConnection struct {
	accountID      string
	accountName    string
	config         IdleConfig
	getCredentials func(accountID string) (*ClientConfig, error)
	isConnected    func() bool // optional: wait for connectivity before reconnecting
	log            zerolog.Logger

	// State
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	doneCh  chan struct{} // Closed when goroutine exits
	folder  string        // Currently watching folder (usually "INBOX")
	client  *imapclient.Client
	events  chan<- MailEvent
}

// newIdleConnection creates a new IDLE connection for an account
func newIdleConnection(accountID, accountName string, config IdleConfig, getCredentials func(accountID string) (*ClientConfig, error)) *IdleConnection {
	return &IdleConnection{
		accountID:      accountID,
		accountName:    accountName,
		config:         config,
		getCredentials: getCredentials,
		log:            logging.WithComponent("imap-idle").With().Str("account", accountName).Logger(),
		folder:         "INBOX",
	}
}

// sendEvent sends an event with timeout to prevent blocking
func (ic *IdleConnection) sendEvent(event MailEvent) {
	select {
	case ic.events <- event:
		// Event sent successfully
	case <-time.After(ic.config.EventSendTimeout):
		ic.log.Warn().
			Str("type", event.Type.String()).
			Msg("Event channel full, dropping event (receiver may be stuck)")
	case <-ic.stopCh:
		// Connection stopping, discard event
	}
}

// Start starts the IDLE loop for this connection
func (ic *IdleConnection) Start(ctx context.Context, events chan<- MailEvent) {
	ic.mu.Lock()
	if ic.running {
		ic.mu.Unlock()
		return
	}
	ic.running = true
	ic.stopCh = make(chan struct{})
	ic.doneCh = make(chan struct{})
	ic.events = events
	ic.mu.Unlock()

	go ic.run(ctx)
}

// Stop stops the IDLE connection with graceful shutdown
func (ic *IdleConnection) Stop() {
	ic.mu.Lock()
	if !ic.running {
		ic.mu.Unlock()
		return
	}

	ic.running = false
	close(ic.stopCh)
	doneCh := ic.doneCh
	timeout := ic.config.ShutdownTimeout
	ic.mu.Unlock()

	// Wait for graceful shutdown with timeout
	if doneCh != nil {
		select {
		case <-doneCh:
			ic.log.Debug().Msg("IDLE connection stopped gracefully")
		case <-time.After(timeout):
			ic.log.Warn().Msg("IDLE connection shutdown timed out, forcing close")
			ic.mu.Lock()
			if ic.client != nil {
				ic.client.Close()
				ic.client = nil
			}
			ic.mu.Unlock()
		}
	}
}

// run is the main IDLE loop
func (ic *IdleConnection) run(ctx context.Context) {
	defer func() {
		ic.mu.Lock()
		ic.running = false
		if ic.client != nil {
			ic.client.Close()
			ic.client = nil
		}
		if ic.doneCh != nil {
			close(ic.doneCh)
		}
		ic.mu.Unlock()
	}()

	backoff := ic.config.ReconnectBackoff
	attempts := 0

	for {
		select {
		case <-ctx.Done():
			ic.log.Debug().Msg("Context cancelled, stopping IDLE")
			return
		case <-ic.stopCh:
			ic.log.Debug().Msg("Stop requested, stopping IDLE")
			return
		default:
		}

		// Stop if offline — processNetworkEvents will restart IDLE
		// when connectivity is restored, avoiding wasteful retries
		if ic.isConnected != nil && !ic.isConnected() {
			ic.log.Debug().Msg("Offline, stopping IDLE (will restart when online)")
			return
		}

		// Connect if needed
		if err := ic.ensureConnected(ctx); err != nil {
			attempts++
			if attempts >= ic.config.MaxReconnectAttempts {
				ic.log.Error().
					Err(err).
					Int("attempts", attempts).
					Msg("Max reconnection attempts reached, giving up")
				return
			}

			ic.log.Warn().
				Err(err).
				Dur("backoff", backoff).
				Int("attempt", attempts).
				Msg("Failed to connect for IDLE, retrying")

			select {
			case <-time.After(backoff):
				backoff = min(backoff*2, ic.config.MaxReconnectBackoff)
				continue
			case <-ctx.Done():
				return
			case <-ic.stopCh:
				return
			}
		}

		// Reset backoff on successful connection
		backoff = ic.config.ReconnectBackoff
		attempts = 0

		// Run IDLE cycle
		if err := ic.idleCycle(ctx); err != nil {
			ic.log.Warn().Err(err).Msg("IDLE cycle failed")
			// Close the connection so we reconnect on next iteration
			ic.mu.Lock()
			if ic.client != nil {
				ic.client.Close()
				ic.client = nil
			}
			ic.mu.Unlock()
		}
	}
}

// ensureConnected ensures we have a valid connection with unilateral data handler
func (ic *IdleConnection) ensureConnected(ctx context.Context) error {
	ic.mu.Lock()
	if ic.client != nil {
		ic.mu.Unlock()
		return nil
	}
	ic.mu.Unlock()

	// Get credentials
	creds, err := ic.getCredentials(ic.accountID)
	if err != nil {
		return err
	}

	// Create client with unilateral data handler for IDLE notifications
	options := &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages != nil {
					ic.log.Info().Uint32("count", *data.NumMessages).Msg("New messages notification (EXISTS)")
					ic.sendEvent(MailEvent{
						Type:      EventNewMail,
						AccountID: ic.accountID,
						Folder:    ic.folder,
						Count:     *data.NumMessages,
					})
				}
			},
			Expunge: func(seqNum uint32) {
				ic.log.Debug().Uint32("seqNum", seqNum).Msg("Message expunged")
				ic.sendEvent(MailEvent{
					Type:      EventExpunge,
					AccountID: ic.accountID,
					Folder:    ic.folder,
					SeqNum:    seqNum,
				})
			},
		},
	}

	addr := net.JoinHostPort(creds.Host, strconv.Itoa(creds.Port))
	var client *imapclient.Client

	// IDLE connections use a longer read timeout (slightly beyond the IDLE cycle)
	// so deadlines don't fire during normal IDLE waiting, but still catch dead connections.
	// Write timeout stays short for login/select/IDLE start commands.
	idleReadTimeout := ic.config.IdleTimeout + 1*time.Minute
	idleWriteTimeout := 30 * time.Second
	connectDialer := &net.Dialer{Timeout: 30 * time.Second}

	switch SecurityType(creds.Security) {
	case SecurityTLS:
		tlsConfig := creds.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{ServerName: creds.Host}
		}
		rawConn, dialErr := tls.DialWithDialer(connectDialer, "tcp", addr, tlsConfig)
		if dialErr != nil {
			return fmt.Errorf("failed to connect with TLS: %w", dialErr)
		}
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  idleReadTimeout,
			writeTimeout: idleWriteTimeout,
		}
		client = imapclient.New(wrappedConn, options)

	case SecurityStartTLS:
		rawConn, dialErr := connectDialer.Dial("tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("failed to connect: %w", dialErr)
		}
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  idleReadTimeout,
			writeTimeout: idleWriteTimeout,
		}
		options.TLSConfig = creds.TLSConfig
		if options.TLSConfig == nil {
			options.TLSConfig = &tls.Config{ServerName: creds.Host}
		}
		client, err = imapclient.NewStartTLS(wrappedConn, options)

	case SecurityNone:
		rawConn, dialErr := connectDialer.Dial("tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("failed to connect: %w", dialErr)
		}
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  idleReadTimeout,
			writeTimeout: idleWriteTimeout,
		}
		client = imapclient.New(wrappedConn, options)

	default:
		tlsConfig := creds.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{ServerName: creds.Host}
		}
		rawConn, dialErr := tls.DialWithDialer(connectDialer, "tcp", addr, tlsConfig)
		if dialErr != nil {
			return fmt.Errorf("failed to connect with TLS: %w", dialErr)
		}
		wrappedConn := &deadlineConn{
			Conn:         rawConn,
			readTimeout:  idleReadTimeout,
			writeTimeout: idleWriteTimeout,
		}
		client = imapclient.New(wrappedConn, options)
	}

	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Wait for greeting
	if err := client.WaitGreeting(); err != nil {
		client.Close()
		return fmt.Errorf("failed to receive greeting: %w", err)
	}

	// Login
	authType := creds.AuthType
	if authType == "" {
		authType = AuthTypePassword
	}

	switch authType {
	case AuthTypeOAuth2:
		saslClient := NewXOAuth2Client(creds.Username, creds.AccessToken)
		if err := client.Authenticate(saslClient); err != nil {
			client.Close()
			return fmt.Errorf("OAuth2 authentication failed: %w", err)
		}
	default:
		saslClient := sasl.NewPlainClient("", creds.Username, creds.Password)
		if err := client.Authenticate(saslClient); err != nil {
			// Fall back to LOGIN command
			if err := client.Login(creds.Username, creds.Password).Wait(); err != nil {
				client.Close()
				return fmt.Errorf("authentication failed: %w", err)
			}
		}
	}

	// Check if server supports IDLE
	if !client.Caps().Has("IDLE") {
		client.Close()
		ic.log.Info().Msg("Server does not support IDLE, falling back to polling only")
		// Return a special error that indicates IDLE is not supported
		return fmt.Errorf("server does not support IDLE")
	}

	// Select INBOX
	selectCmd := client.Select(ic.folder, nil)
	if _, err := selectCmd.Wait(); err != nil {
		client.Close()
		return fmt.Errorf("failed to select INBOX: %w", err)
	}

	ic.mu.Lock()
	ic.client = client
	ic.mu.Unlock()

	ic.log.Info().Msg("IDLE connection established")
	return nil
}

// idleCycle runs a single IDLE cycle with timeout
func (ic *IdleConnection) idleCycle(ctx context.Context) error {
	ic.mu.Lock()
	client := ic.client
	if client == nil {
		ic.mu.Unlock()
		return nil
	}
	ic.mu.Unlock()

	// Health check: verify connection is alive before entering IDLE
	if ic.config.HealthCheckEnabled {
		ic.log.Debug().Msg("Running connection health check (NOOP)")
		if err := client.Noop().Wait(); err != nil {
			return fmt.Errorf("health check failed (connection may be dead): %w", err)
		}
	}

	ic.log.Debug().Msg("Starting IDLE")

	// Start IDLE command
	idleCmd, err := client.Idle()
	if err != nil {
		return fmt.Errorf("failed to start IDLE: %w", err)
	}

	// Set up timeout timer
	timer := time.NewTimer(ic.config.IdleTimeout)
	defer timer.Stop()

	// Wait for timeout or stop signal
	// Note: Unilateral data (EXISTS, EXPUNGE) is handled by the UnilateralDataHandler
	// we set up when creating the client
	select {
	case <-ctx.Done():
		ic.log.Debug().Msg("Context cancelled during IDLE")
		idleCmd.Close()
		return nil

	case <-ic.stopCh:
		ic.log.Debug().Msg("Stop requested during IDLE")
		idleCmd.Close()
		return nil

	case <-timer.C:
		// IDLE timeout - restart to keep connection alive
		ic.log.Debug().Msg("IDLE timeout, restarting")
		if err := idleCmd.Close(); err != nil {
			return err
		}
		return nil
	}
}

// IdleManager manages IDLE connections for multiple accounts
type IdleManager struct {
	config         IdleConfig
	getCredentials func(accountID string) (*ClientConfig, error)
	isConnected    func() bool // optional: propagated to connections
	log            zerolog.Logger

	// Connections per account
	connections map[string]*IdleConnection
	mu          sync.Mutex

	// Event channel
	events chan MailEvent

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewIdleManager creates a new IDLE manager
func NewIdleManager(config IdleConfig, getCredentials func(accountID string) (*ClientConfig, error)) *IdleManager {
	return &IdleManager{
		config:         config,
		getCredentials: getCredentials,
		log:            logging.WithComponent("idle-manager"),
		connections:    make(map[string]*IdleConnection),
		events:         make(chan MailEvent, 100),
	}
}

// SetConnectivityCheck sets a function to check network connectivity.
// When set, IDLE connections will skip reconnect attempts when offline
// to avoid wasted connection attempts and unnecessary error logging.
func (m *IdleManager) SetConnectivityCheck(check func() bool) {
	m.isConnected = check
}

// Start starts the IDLE manager
func (m *IdleManager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.log.Info().Msg("IDLE manager started")
}

// Stop stops all IDLE connections
func (m *IdleManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	for accountID, conn := range m.connections {
		m.log.Debug().Str("account", accountID).Msg("Stopping IDLE connection")
		conn.Stop()
	}
	m.connections = make(map[string]*IdleConnection)
	m.mu.Unlock()

	m.wg.Wait()
	m.log.Info().Msg("IDLE manager stopped")
}

// Events returns the channel for receiving mail events
func (m *IdleManager) Events() <-chan MailEvent {
	return m.events
}

// StartAccount starts IDLE for a specific account
func (m *IdleManager) StartAccount(accountID, accountName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already running
	if conn, exists := m.connections[accountID]; exists {
		conn.mu.Lock()
		running := conn.running
		conn.mu.Unlock()
		if running {
			m.log.Debug().Str("account", accountName).Msg("IDLE already running for account")
			return
		}
		// Goroutine exited (e.g., max reconnect attempts reached) — remove stale entry
		m.log.Debug().Str("account", accountName).Msg("Replacing dead IDLE connection")
		delete(m.connections, accountID)
	}

	// Create and start IDLE connection
	conn := newIdleConnection(accountID, accountName, m.config, m.getCredentials)
	conn.isConnected = m.isConnected
	m.connections[accountID] = conn

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		conn.Start(m.ctx, m.events)
	}()

	m.log.Info().Str("account", accountName).Msg("Started IDLE for account")
}

// StopAccount stops IDLE for a specific account
func (m *IdleManager) StopAccount(accountID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if conn, exists := m.connections[accountID]; exists {
		conn.Stop()
		delete(m.connections, accountID)
		m.log.Info().Str("accountID", accountID).Msg("Stopped IDLE for account")
	}
}

// RestartAccount restarts IDLE for a specific account
func (m *IdleManager) RestartAccount(accountID, accountName string) {
	m.StopAccount(accountID)
	m.StartAccount(accountID, accountName)
}
