package certificate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func generateTestCert(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.example.com", Organization: []string{"Test Org"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"test.example.com"},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	return derBytes
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory DB: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS trusted_certificates (
		id TEXT PRIMARY KEY,
		fingerprint TEXT NOT NULL UNIQUE,
		host TEXT NOT NULL,
		subject TEXT,
		issuer TEXT,
		not_before TEXT,
		not_after TEXT,
		accepted_at DATETIME
	)`)
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

func TestFingerprint(t *testing.T) {
	der := generateTestCert(t)
	fp := Fingerprint(der)

	if len(fp) != 64 {
		t.Fatalf("Fingerprint length = %d, want 64", len(fp))
	}

	// Verify it's hex
	for _, c := range fp {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("Fingerprint contains non-hex char: %c", c)
		}
	}
}

func TestFormatFingerprint(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abcd1234", "AB:CD:12:34"},
		{"aa", "AA"},
		{"aabb", "AA:BB"},
		{"a", "A"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := FormatFingerprint(tt.input)
			if got != tt.want {
				t.Fatalf("FormatFingerprint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractCertInfo(t *testing.T) {
	der := generateTestCert(t)
	info := ExtractCertInfo(der, errors.New("test error"))

	if info.Subject == "" {
		t.Fatal("Subject should not be empty")
	}
	if info.Issuer == "" {
		t.Fatal("Issuer should not be empty")
	}
	if len(info.DNSNames) == 0 {
		t.Fatal("DNSNames should not be empty")
	}
	if info.DNSNames[0] != "test.example.com" {
		t.Fatalf("DNSNames[0] = %q, want %q", info.DNSNames[0], "test.example.com")
	}
	if info.NotBefore == "" {
		t.Fatal("NotBefore should not be empty")
	}
	if info.NotAfter == "" {
		t.Fatal("NotAfter should not be empty")
	}
	if info.Fingerprint == "" {
		t.Fatal("Fingerprint should not be empty")
	}
	if info.IsExpired {
		t.Fatal("IsExpired = true, want false (cert valid for 24h)")
	}
}

func TestFormatDN(t *testing.T) {
	tests := []struct {
		name string
		cn   string
		org  []string
		want string
	}{
		{"cn and org", "example.com", []string{"Org"}, "example.com (Org)"},
		{"cn only", "example.com", nil, "example.com"},
		{"org only", "", []string{"Org"}, "Org"},
		{"neither", "", nil, "(unknown)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDN(tt.cn, tt.org)
			if got != tt.want {
				t.Fatalf("formatDN(%q, %v) = %q, want %q", tt.cn, tt.org, got, tt.want)
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil error", nil, "unknown error"},
		{"unknown authority", errors.New("x509: certificate signed by unknown authority"), "self-signed or unknown certificate authority"},
		{"expired", errors.New("x509: certificate has expired or is not yet valid"), "certificate has expired"},
		{"random error", errors.New("something went wrong"), "something went wrong"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.want {
				t.Fatalf("classifyError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestAcceptSession(t *testing.T) {
	store := openTestStore(t)

	fp := "aabbccdd11223344aabbccdd11223344aabbccdd11223344aabbccdd11223344"
	store.AcceptSession(fp)

	if !store.IsTrusted(fp) {
		t.Fatal("IsTrusted = false after AcceptSession, want true")
	}
}

func TestIsTrustedDefault(t *testing.T) {
	store := openTestStore(t)

	fp := "0000000000000000000000000000000000000000000000000000000000000000"
	if store.IsTrusted(fp) {
		t.Fatal("IsTrusted = true for unknown fingerprint, want false")
	}
}

// TestBuildTLSConfigDynamic exercises the host-agnostic TOFU verifier used by
// the DAV transports. Drives VerifyConnection directly (no TLS server needed):
// an untrusted self-signed cert is rejected with a structured *Error; once its
// fingerprint is trusted it passes; an empty chain errors.
func TestBuildTLSConfigDynamic(t *testing.T) {
	store := openTestStore(t)
	der := generateTestCert(t)
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	cfg := BuildTLSConfigDynamic(store)
	if cfg.VerifyConnection == nil {
		t.Fatal("VerifyConnection is nil")
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must be true (the callback does the real verification)")
	}

	cs := tls.ConnectionState{
		ServerName:       "test.example.com",
		PeerCertificates: []*x509.Certificate{cert},
	}

	// Untrusted self-signed → structured *Error.
	err = cfg.VerifyConnection(cs)
	if err == nil {
		t.Fatal("expected error for untrusted self-signed cert")
	}
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}

	// Trust the fingerprint → now accepted.
	store.AcceptSession(Fingerprint(der))
	if err := cfg.VerifyConnection(cs); err != nil {
		t.Fatalf("expected store-trusted cert to pass, got %v", err)
	}

	// Empty chain → error.
	if err := cfg.VerifyConnection(tls.ConnectionState{ServerName: "test.example.com"}); err == nil {
		t.Fatal("expected error for empty PeerCertificates")
	}
}

func TestErrorInterface(t *testing.T) {
	info := &CertificateInfo{
		Fingerprint: "abcd1234",
	}
	certErr := &Error{
		Info:   info,
		Reason: "test reason",
	}

	// Verify it implements the error interface
	var err error = certErr
	if err.Error() == "" {
		t.Fatal("Error() should return non-empty string")
	}

	expected := "untrusted certificate: test reason (fingerprint: abcd1234)"
	if err.Error() != expected {
		t.Fatalf("Error() = %q, want %q", err.Error(), expected)
	}
}
