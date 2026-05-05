package smime

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func generateTestPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:   big.NewInt(1),
		Subject:        pkix.Name{CommonName: "test@example.com"},
		NotBefore:      time.Now().Add(-1 * time.Hour),
		NotAfter:       time.Now().Add(24 * time.Hour),
		EmailAddresses: []string{"test@example.com"},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	return string(pemBlock)
}

func TestParseCertChainFromPEM(t *testing.T) {
	tests := []struct {
		name      string
		input     func(t *testing.T) string
		wantCount int
		wantErr   bool
	}{
		{
			name: "single cert",
			input: func(t *testing.T) string {
				return generateTestPEM(t)
			},
			wantCount: 1,
		},
		{
			name: "multiple certs",
			input: func(t *testing.T) string {
				return generateTestPEM(t) + generateTestPEM(t)
			},
			wantCount: 2,
		},
		{
			name: "empty string",
			input: func(t *testing.T) string {
				return ""
			},
			wantErr: true,
		},
		{
			name: "invalid data",
			input: func(t *testing.T) string {
				return "not a cert"
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input(t)
			certs, err := ParseCertChainFromPEM(input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(certs) != tt.wantCount {
				t.Errorf("got %d certs, want %d", len(certs), tt.wantCount)
			}
		})
	}
}

func TestIsSMIMESigned(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{
			name:        "pkcs7-signature protocol",
			contentType: `multipart/signed; protocol="application/pkcs7-signature"; boundary=foo`,
			want:        true,
		},
		{
			name:        "x-pkcs7-signature protocol",
			contentType: `multipart/signed; protocol="application/x-pkcs7-signature"; boundary=foo`,
			want:        true,
		},
		{
			name:        "signed-data smime-type",
			contentType: `application/pkcs7-mime; smime-type=signed-data`,
			want:        true,
		},
		{
			name:        "text/plain",
			contentType: "text/plain",
			want:        false,
		},
		{
			name:        "empty string",
			contentType: "",
			want:        false,
		},
		{
			name:        "pgp-signature protocol",
			contentType: `multipart/signed; protocol="application/pgp-signature"; boundary=foo`,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSMIMESigned(tt.contentType)
			if got != tt.want {
				t.Errorf("IsSMIMESigned(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestIsSMIMEEncrypted(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{
			name:        "enveloped-data",
			contentType: `application/pkcs7-mime; smime-type=enveloped-data`,
			want:        true,
		},
		{
			name:        "x-pkcs7-mime enveloped-data",
			contentType: `application/x-pkcs7-mime; smime-type=enveloped-data`,
			want:        true,
		},
		{
			name:        "text/plain",
			contentType: "text/plain",
			want:        false,
		},
		{
			name:        "empty string",
			contentType: "",
			want:        false,
		},
		{
			name:        "signed-data not encrypted",
			contentType: `application/pkcs7-mime; smime-type=signed-data`,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSMIMEEncrypted(tt.contentType)
			if got != tt.want {
				t.Errorf("IsSMIMEEncrypted(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}
