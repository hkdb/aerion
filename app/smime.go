package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/hkdb/aerion/internal/smime"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// PickSMIMECertificateFile opens a file picker for .p12/.pfx files and returns the path
func (a *App) PickSMIMECertificateFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select S/MIME Certificate",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "PKCS#12 Files (*.p12, *.pfx)",
				Pattern:     "*.p12;*.pfx",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file dialog: %w", err)
	}
	return path, nil
}

// ImportSMIMECertificateFromPath imports a PKCS#12 certificate from a file path with password
func (a *App) ImportSMIMECertificateFromPath(accountID, filePath, password string) (*smime.ImportResult, error) {
	data, err := readCertFile(filePath)
	if err != nil {
		return nil, err
	}

	privateKeyPEM, certChainPEM, cert, err := smime.ImportPKCS12(data, password)
	if err != nil {
		return nil, fmt.Errorf("failed to import certificate: %w", err)
	}

	return a.storeSMIMECert(accountID, privateKeyPEM, certChainPEM, cert)
}

// ImportSMIMECertificateFromPathBER imports a BER-encoded PKCS#12 certificate
// by converting it to DER first. Called after user confirms the conversion.
func (a *App) ImportSMIMECertificateFromPathBER(accountID, filePath, password string) (*smime.ImportResult, error) {
	data, err := readCertFile(filePath)
	if err != nil {
		return nil, err
	}

	privateKeyPEM, certChainPEM, cert, err := smime.ImportPKCS12BER(data, password)
	if err != nil {
		return nil, fmt.Errorf("failed to import certificate: %w", err)
	}

	return a.storeSMIMECert(accountID, privateKeyPEM, certChainPEM, cert)
}

func readCertFile(filePath string) ([]byte, error) {
	if filePath == "" {
		return nil, fmt.Errorf("no file selected")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file: %w", err)
	}
	return data, nil
}

func (a *App) storeSMIMECert(accountID string, privateKeyPEM []byte, certChainPEM string, cert *smime.Certificate) (*smime.ImportResult, error) {
	cert.AccountID = accountID

	// Validate that the cert email matches the account or one of its aliases
	if err := a.validateCertEmailForAccount(accountID, cert.Email); err != nil {
		return nil, err
	}

	// Check if this is the first cert for this account (make it default)
	existing, err := a.smimeStore.ListCertificates(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing certificates: %w", err)
	}
	if len(existing) == 0 {
		cert.IsDefault = true
	}

	// Store the certificate metadata
	if err := a.smimeStore.SaveCertificate(cert, certChainPEM); err != nil {
		return nil, fmt.Errorf("failed to save certificate: %w", err)
	}

	// Store the private key securely
	if err := a.credStore.SetSMIMEPrivateKey(cert.ID, privateKeyPEM); err != nil {
		// Rollback: delete the cert if key storage fails
		a.smimeStore.DeleteCertificate(cert.ID)
		return nil, fmt.Errorf("failed to store private key: %w", err)
	}

	// If this is the first cert, set it as default on the account
	if cert.IsDefault {
		a.smimeStore.SetDefaultCertificate(accountID, cert.ID)
	}

	// Count chain length (leaf + intermediates)
	chainLength := 1
	if certChainPEM != "" {
		certs, _ := smime.ParseCertChainFromPEM(certChainPEM)
		if certs != nil {
			chainLength = len(certs)
		}
	}

	return &smime.ImportResult{
		Certificate: cert,
		ChainLength: chainLength,
	}, nil
}

// ListSMIMECertificates returns all S/MIME certificates for an account
func (a *App) ListSMIMECertificates(accountID string) ([]*smime.Certificate, error) {
	certs, err := a.smimeStore.ListCertificates(accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to list certificates: %w", err)
	}
	if certs == nil {
		return []*smime.Certificate{}, nil
	}
	return certs, nil
}

// DeleteSMIMECertificate removes an S/MIME certificate and its private key
func (a *App) DeleteSMIMECertificate(certID string) error {
	// Delete private key first
	if err := a.credStore.DeleteSMIMEPrivateKey(certID); err != nil {
		return fmt.Errorf("failed to delete private key: %w", err)
	}

	// Delete certificate
	if err := a.smimeStore.DeleteCertificate(certID); err != nil {
		return fmt.Errorf("failed to delete certificate: %w", err)
	}

	return nil
}

// SetDefaultSMIMECertificate sets the default signing certificate for an account
func (a *App) SetDefaultSMIMECertificate(accountID, certID string) error {
	return a.smimeStore.SetDefaultCertificate(accountID, certID)
}

// GetSMIMESignPolicy returns the signing policy for an account
func (a *App) GetSMIMESignPolicy(accountID string) (string, error) {
	return a.smimeStore.GetSignPolicy(accountID)
}

// SetSMIMESignPolicy sets the signing policy ('never', 'always')
func (a *App) SetSMIMESignPolicy(accountID, policy string) error {
	switch policy {
	case "never", "always":
		return a.smimeStore.SetSignPolicy(accountID, policy)
	default:
		return fmt.Errorf("invalid sign policy: %s", policy)
	}
}

// ListSenderCerts returns all cached sender certificates
func (a *App) ListSenderCerts() ([]*smime.SenderCert, error) {
	certs, err := a.smimeStore.ListAllSenderCerts()
	if err != nil {
		return nil, fmt.Errorf("failed to list sender certificates: %w", err)
	}
	if certs == nil {
		return []*smime.SenderCert{}, nil
	}
	return certs, nil
}

// DeleteSenderCert removes a cached sender certificate
func (a *App) DeleteSenderCert(certID string) error {
	return a.smimeStore.DeleteSenderCert(certID)
}

// HasSMIMECertificate returns whether an account has a default S/MIME certificate configured
func (a *App) HasSMIMECertificate(accountID string) bool {
	return a.composeOps.hasSMIMECertificate(accountID)
}

// GetSMIMECertificateForEmail returns the S/MIME certificate matching the given email.
// Returns nil if no matching certificate is found.
func (a *App) GetSMIMECertificateForEmail(accountID, email string) (*smime.Certificate, error) {
	cert, _, err := a.smimeStore.GetCertificateByEmail(accountID, email)
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate for email: %w", err)
	}
	return cert, nil
}

// shouldSignMessage determines whether a message should be S/MIME signed
func (a *App) shouldSignMessage(accountID string, perMessageOverride bool) bool {
	return a.composeOps.shouldSignMessage(accountID, perMessageOverride)
}

// shouldEncryptMessage determines whether a message should be S/MIME encrypted
func (a *App) shouldEncryptMessage(accountID string, perMessageOverride bool) bool {
	return a.composeOps.shouldEncryptMessage(accountID, perMessageOverride)
}

// GetSMIMEEncryptPolicy returns the encryption policy for an account
func (a *App) GetSMIMEEncryptPolicy(accountID string) (string, error) {
	return a.smimeStore.GetEncryptPolicy(accountID)
}

// SetSMIMEEncryptPolicy sets the encryption policy ('never', 'always')
func (a *App) SetSMIMEEncryptPolicy(accountID, policy string) error {
	switch policy {
	case "never", "always":
		return a.smimeStore.SetEncryptPolicy(accountID, policy)
	default:
		return fmt.Errorf("invalid encrypt policy: %s", policy)
	}
}

// CheckRecipientCerts checks which recipients have S/MIME certificates available
func (a *App) CheckRecipientCerts(emails []string) (map[string]bool, error) {
	certPEMs, err := a.smimeStore.GetSenderCertPEMs(emails)
	if err != nil {
		return nil, fmt.Errorf("failed to check recipient certs: %w", err)
	}

	result := make(map[string]bool)
	for _, email := range emails {
		_, hasCert := certPEMs[email]
		result[email] = hasCert
	}
	return result, nil
}

// PickRecipientCertFile opens a file picker for certificate files (.pem, .cer, .crt, .der)
func (a *App) PickRecipientCertFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: "Select Recipient Certificate",
		Filters: []wailsRuntime.FileFilter{
			{
				DisplayName: "Certificate Files (*.pem, *.cer, *.crt, *.der)",
				Pattern:     "*.pem;*.cer;*.crt;*.der",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file dialog: %w", err)
	}
	return path, nil
}

// ImportRecipientCert imports a recipient's public certificate from a file
func (a *App) ImportRecipientCert(email, filePath string) error {
	if filePath == "" {
		return fmt.Errorf("no file selected")
	}
	if email == "" {
		return fmt.Errorf("email address required")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read certificate file: %w", err)
	}

	return a.smimeStore.ImportSenderCertFromFile(email, data)
}

// validateCertEmailForAccount checks that the given email matches the account email
// or one of its identity aliases. Returns an error if no match is found.
func (a *App) validateCertEmailForAccount(accountID, certEmail string) error {
	if certEmail == "" {
		return nil // No email to validate
	}

	certEmailLower := strings.ToLower(strings.TrimSpace(certEmail))

	// Check account email
	acc, err := a.accountStore.Get(accountID)
	if err != nil {
		return fmt.Errorf("failed to get account: %w", err)
	}
	if acc != nil && strings.ToLower(strings.TrimSpace(acc.Email)) == certEmailLower {
		return nil
	}

	// Check identity emails
	identities, err := a.accountStore.GetIdentities(accountID)
	if err != nil {
		return fmt.Errorf("failed to get identities: %w", err)
	}
	for _, id := range identities {
		if strings.ToLower(strings.TrimSpace(id.Email)) == certEmailLower {
			return nil
		}
	}

	return fmt.Errorf("certificate email %q does not match this account or any of its aliases", certEmail)
}
