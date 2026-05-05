// Package smime provides S/MIME signing and verification for email messages
package smime

import "time"

// SignatureStatus represents the S/MIME verification result
type SignatureStatus string

const (
	StatusNone          SignatureStatus = ""               // Not S/MIME
	StatusSigned        SignatureStatus = "signed"         // Valid signature, trusted chain
	StatusInvalid       SignatureStatus = "invalid"        // Signature does not verify
	StatusUnknownSigner SignatureStatus = "unknown_signer" // Valid sig, untrusted CA
	StatusSelfSigned    SignatureStatus = "self_signed"    // Valid sig, self-signed cert
	StatusExpiredCert   SignatureStatus = "expired_cert"   // Valid sig, expired cert
)

// Certificate represents a user's imported S/MIME certificate
type Certificate struct {
	ID           string    `json:"id"`
	AccountID    string    `json:"accountId"`
	Email        string    `json:"email"`
	Subject      string    `json:"subject"`
	Issuer       string    `json:"issuer"`
	SerialNumber string    `json:"serialNumber"`
	Fingerprint  string    `json:"fingerprint"`
	NotBefore    time.Time `json:"notBefore"`
	NotAfter     time.Time `json:"notAfter"`
	IsDefault    bool      `json:"isDefault"`
	IsExpired    bool      `json:"isExpired"`    // Computed, not stored
	IsSelfSigned bool      `json:"isSelfSigned"` // Computed, not stored
	CreatedAt    time.Time `json:"createdAt"`
}

// SenderCert represents a cached public certificate from a signed message sender
type SenderCert struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Subject      string    `json:"subject"`
	Issuer       string    `json:"issuer"`
	SerialNumber string    `json:"serialNumber"`
	Fingerprint  string    `json:"fingerprint"`
	NotBefore    time.Time `json:"notBefore"`
	NotAfter     time.Time `json:"notAfter"`
	CollectedAt  time.Time `json:"collectedAt"`
	LastSeenAt   time.Time `json:"lastSeenAt"`
}

// SignatureResult holds the verification result for a message
type SignatureResult struct {
	Status       SignatureStatus `json:"status"`
	SignerEmail  string          `json:"signerEmail"`
	SignerName   string          `json:"signerName"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
}

// ImportResult holds the result of a PKCS#12 certificate import
type ImportResult struct {
	Certificate *Certificate `json:"certificate"`
	ChainLength int          `json:"chainLength"`
}
