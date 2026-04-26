package smime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestImportPKCS12_DER_SpecialCharPassword verifies that a standard DER-encoded
// PKCS#12 file with special characters in the password imports correctly.
func TestImportPKCS12_DER_SpecialCharPassword(t *testing.T) {
	p12Path := generateTestPKCS12(t)
	data, err := os.ReadFile(p12Path)
	if err != nil {
		t.Fatalf("failed to read test p12: %v", err)
	}

	privPEM, chainPEM, cert, err := ImportPKCS12(data, "p^ss+w=rd&")
	if err != nil {
		t.Fatalf("DER import failed: %v", err)
	}
	if len(privPEM) == 0 {
		t.Error("expected non-empty private key PEM")
	}
	if chainPEM == "" {
		t.Error("expected non-empty certificate chain PEM")
	}
	if cert.ID == "" {
		t.Error("expected non-empty cert ID")
	}
	if cert.Subject == "" {
		t.Error("expected non-empty subject")
	}
}

// TestImportPKCS12_BER_IndefiniteLength verifies that a BER-encoded PKCS#12 file
// with indefinite-length encoding imports correctly via the fallback path.
func TestImportPKCS12_BER_IndefiniteLength(t *testing.T) {
	p12Path := generateTestPKCS12(t)
	data, err := os.ReadFile(p12Path)
	if err != nil {
		t.Fatalf("failed to read test p12: %v", err)
	}

	// Convert DER to BER by replacing top-level SEQUENCE's definite length
	// with indefinite length + end-of-contents marker
	if data[0] != 0x30 {
		t.Fatal("expected SEQUENCE tag at start of PKCS#12")
	}

	// Parse DER length to find content start
	contentStart := 2
	if data[1]&0x80 != 0 {
		numBytes := int(data[1] & 0x7f)
		contentStart = 2 + numBytes
	}
	content := data[contentStart:]

	// Build BER: SEQUENCE (indefinite) + content + end-of-contents
	var berData []byte
	berData = append(berData, 0x30, 0x80) // SEQUENCE indefinite
	berData = append(berData, content...)
	berData = append(berData, 0x00, 0x00) // end-of-contents

	// Verify standard ImportPKCS12 fails with BER data
	_, _, _, berErr := ImportPKCS12(berData, "p^ss+w=rd&")
	if !IsBEREncodingError(berErr) {
		t.Fatalf("expected BER encoding error, got: %v", berErr)
	}

	// ImportPKCS12BER should succeed after BER-to-DER conversion
	privPEM, chainPEM, cert, err := ImportPKCS12BER(berData, "p^ss+w=rd&")
	if err != nil {
		t.Fatalf("BER import failed: %v", err)
	}
	if len(privPEM) == 0 {
		t.Error("expected non-empty private key PEM")
	}
	if chainPEM == "" {
		t.Error("expected non-empty certificate chain PEM")
	}
	if cert.ID == "" {
		t.Error("expected non-empty cert ID")
	}
}

// TestImportPKCS12_WrongPassword verifies that wrong password returns an error.
func TestImportPKCS12_WrongPassword(t *testing.T) {
	p12Path := generateTestPKCS12(t)
	data, err := os.ReadFile(p12Path)
	if err != nil {
		t.Fatalf("failed to read test p12: %v", err)
	}

	_, _, _, err = ImportPKCS12(data, "wrongpassword")
	if err == nil {
		t.Error("expected error for wrong password")
	}
}

// generateTestPKCS12 creates a self-signed cert + PKCS#12 file in a temp dir.
// Requires openssl in PATH.
func generateTestPKCS12(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not found in PATH")
	}

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test.key")
	certPath := filepath.Join(dir, "test.crt")
	p12Path := filepath.Join(dir, "test.p12")

	// Generate self-signed cert
	cmd := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:2048",
		"-keyout", keyPath, "-out", certPath, "-days", "30", "-nodes",
		"-subj", "/CN=Test User/emailAddress=test@example.com")
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to generate test cert: %v", err)
	}

	// Export as PKCS#12 with special char password
	cmd = exec.Command("openssl", "pkcs12", "-export",
		"-in", certPath, "-inkey", keyPath, "-out", p12Path,
		"-passout", "pass:p^ss+w=rd&")
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create test p12: %v", err)
	}

	return p12Path
}
