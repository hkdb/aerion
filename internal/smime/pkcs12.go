package smime

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	gopkcs12 "software.sslmate.com/src/go-pkcs12"
)

// ImportPKCS12 parses a PKCS#12 file and extracts the certificate chain and private key.
// Returns the private key as PEM bytes, the certificate chain as PEM string,
// and parsed certificate metadata.
func ImportPKCS12(data []byte, password string) (privateKeyPEM []byte, certChainPEM string, cert *Certificate, err error) {
	privateKey, leafCert, caCerts, err := gopkcs12.DecodeChain(data, password)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to decode PKCS#12: %w", err)
	}

	// Marshal the private key to PKCS#8 DER
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	// Encode private key to PEM
	privateKeyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})

	// Build certificate chain PEM (leaf first, then intermediates)
	var chainPEM []byte
	chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: leafCert.Raw,
	})...)
	for _, ca := range caCerts {
		chainPEM = append(chainPEM, pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: ca.Raw,
		})...)
	}
	certChainPEM = string(chainPEM)

	// Extract email from certificate
	email := extractEmailFromCert(leafCert)

	// Build certificate metadata
	cert = &Certificate{
		ID:           uuid.New().String(),
		Email:        email,
		Subject:      leafCert.Subject.String(),
		Issuer:       leafCert.Issuer.String(),
		SerialNumber: leafCert.SerialNumber.String(),
		Fingerprint:  certificateFingerprint(leafCert.Raw),
		NotBefore:    leafCert.NotBefore,
		NotAfter:     leafCert.NotAfter,
		IsExpired:    time.Now().After(leafCert.NotAfter),
		IsSelfSigned: bytes.Equal(leafCert.RawIssuer, leafCert.RawSubject),
		CreatedAt:    time.Now(),
	}

	return privateKeyPEM, certChainPEM, cert, nil
}

// IsBEREncodingError returns true if the error indicates BER encoding
// (indefinite-length) that needs conversion to DER.
func IsBEREncodingError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "indefinite length")
}

// ImportPKCS12BER converts BER-encoded PKCS#12 data to DER, then imports it.
func ImportPKCS12BER(data []byte, password string) (privateKeyPEM []byte, certChainPEM string, cert *Certificate, err error) {
	derData, convertErr := berToDER(data)
	if convertErr != nil {
		return nil, "", nil, fmt.Errorf("failed to convert BER to DER: %w", convertErr)
	}
	return ImportPKCS12(derData, password)
}
