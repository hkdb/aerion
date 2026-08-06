package smtp

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMechanismAdvertised(t *testing.T) {
	tests := []struct {
		name       string
		mechanisms string
		mechanism  string
		want       bool
	}{
		{"login-only server advertises LOGIN", "GSSAPI NTLM LOGIN", "LOGIN", true},
		{"login-only server does not advertise PLAIN", "GSSAPI NTLM LOGIN", "PLAIN", false},
		{"plain and login both advertised: PLAIN", "PLAIN LOGIN", "PLAIN", true},
		{"plain and login both advertised: LOGIN", "PLAIN LOGIN", "LOGIN", true},
		{"empty advertisement", "", "PLAIN", false},
		{"case-insensitive match", "login plain", "PLAIN", true},
		{"no substring false positive", "XPLAIN", "PLAIN", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mechanismAdvertised(tt.mechanisms, tt.mechanism)
			if got != tt.want {
				t.Errorf("mechanismAdvertised(%q, %q) = %v, want %v", tt.mechanisms, tt.mechanism, got, tt.want)
			}
		})
	}
}

// fakeSMTPServer speaks just enough SMTP to accept one connection, advertise
// the given AUTH mechanisms on EHLO, and accept any AUTH attempt (or reject
// every attempt when rejectAuth is set). It records each AUTH command line it
// receives on authCmd.
func fakeSMTPServer(t *testing.T, authAdvert string, rejectAuth bool, authCmd chan<- string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		fmt.Fprintf(conn, "220 fake ESMTP ready\r\n")
		for {
			line, readErr := r.ReadString('\n')
			if readErr != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				fmt.Fprintf(conn, "250-fake\r\n250-AUTH %s\r\n250 8BITMIME\r\n", authAdvert)
			case strings.HasPrefix(cmd, "AUTH LOGIN"):
				authCmd <- strings.TrimSpace(line)
				if rejectAuth {
					fmt.Fprintf(conn, "504 5.5.4 Unrecognized authentication type\r\n")
					continue
				}
				// Base64 "Username:" / "Password:" challenges
				fmt.Fprintf(conn, "334 VXNlcm5hbWU6\r\n")
				if _, err := r.ReadString('\n'); err != nil {
					return
				}
				fmt.Fprintf(conn, "334 UGFzc3dvcmQ6\r\n")
				if _, err := r.ReadString('\n'); err != nil {
					return
				}
				fmt.Fprintf(conn, "235 2.7.0 Authentication successful\r\n")
			case strings.HasPrefix(cmd, "AUTH PLAIN"):
				authCmd <- strings.TrimSpace(line)
				if rejectAuth {
					fmt.Fprintf(conn, "504 5.5.4 Unrecognized authentication type\r\n")
					continue
				}
				fmt.Fprintf(conn, "235 2.7.0 Authentication successful\r\n")
			case strings.HasPrefix(cmd, "QUIT"):
				fmt.Fprintf(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 ok\r\n")
			}
		}
	}()
	return ln
}

// TestLoginMechanismSelection verifies which SASL mechanism the client puts on
// the wire, per advertisement and per manual override (#355).
func TestLoginMechanismSelection(t *testing.T) {
	tests := []struct {
		name          string
		authAdvert    string
		authMechanism string
		wantCmd       string
	}{
		{"auto picks LOGIN when PLAIN not advertised", "GSSAPI NTLM LOGIN", "", "AUTH LOGIN"},
		{"auto picks PLAIN when advertised", "PLAIN LOGIN", "", "AUTH PLAIN"},
		{"manual login override beats PLAIN advertisement", "PLAIN LOGIN", "login", "AUTH LOGIN"},
		{"manual plain override", "GSSAPI NTLM LOGIN", "plain", "AUTH PLAIN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authCmd := make(chan string, 1)
			ln := fakeSMTPServer(t, tt.authAdvert, false, authCmd)
			defer ln.Close()

			addr := ln.Addr().(*net.TCPAddr)
			config := DefaultConfig()
			config.Host = "127.0.0.1"
			config.Port = addr.Port
			config.Security = SecurityNone
			config.Username = "user"
			config.Password = "pass"
			config.AuthMechanism = tt.authMechanism
			config.ConnectTimeout = 5 * time.Second
			config.ReadTimeout = 5 * time.Second
			config.WriteTimeout = 5 * time.Second

			client := NewClient(config)
			if err := client.Connect(); err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer client.Close()
			if err := client.Login(); err != nil {
				t.Fatalf("login: %v", err)
			}

			select {
			case got := <-authCmd:
				if !strings.HasPrefix(strings.ToUpper(got), tt.wantCmd) {
					t.Errorf("server received %q, want prefix %q", got, tt.wantCmd)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no AUTH command received")
			}
		})
	}
}

// TestForcedMechanismFailsWithoutFallback verifies that a manually forced
// mechanism which the server rejects produces a clean authentication error and
// does NOT fall back to another mechanism.
func TestForcedMechanismFailsWithoutFallback(t *testing.T) {
	tests := []struct {
		name          string
		authMechanism string
		wantCmd       string
	}{
		{"forced LOGIN rejected", "login", "AUTH LOGIN"},
		{"forced PLAIN rejected", "plain", "AUTH PLAIN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authCmd := make(chan string, 4)
			ln := fakeSMTPServer(t, "GSSAPI NTLM", true, authCmd)
			defer ln.Close()

			addr := ln.Addr().(*net.TCPAddr)
			config := DefaultConfig()
			config.Host = "127.0.0.1"
			config.Port = addr.Port
			config.Security = SecurityNone
			config.Username = "user"
			config.Password = "pass"
			config.AuthMechanism = tt.authMechanism
			config.ConnectTimeout = 5 * time.Second
			config.ReadTimeout = 5 * time.Second
			config.WriteTimeout = 5 * time.Second

			client := NewClient(config)
			if err := client.Connect(); err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer client.Close()

			err := client.Login()
			if err == nil {
				t.Fatal("Login() succeeded, want authentication error")
			}
			if !strings.Contains(err.Error(), "authentication failed") {
				t.Errorf("error = %q, want it to contain %q", err, "authentication failed")
			}

			// Exactly one AUTH attempt — the forced mechanism, no fallback.
			select {
			case got := <-authCmd:
				if !strings.HasPrefix(strings.ToUpper(got), tt.wantCmd) {
					t.Errorf("server received %q, want prefix %q", got, tt.wantCmd)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no AUTH command received")
			}
			select {
			case extra := <-authCmd:
				t.Errorf("unexpected second AUTH attempt: %q", extra)
			case <-time.After(100 * time.Millisecond):
				// no fallback attempt — expected
			}
		})
	}
}
