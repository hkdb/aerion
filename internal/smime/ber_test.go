package smime

import (
	"bytes"
	"encoding/asn1"
	"testing"
)

// TestBerToDER_AlreadyDER verifies that valid DER data passes through unchanged.
func TestBerToDER_AlreadyDER(t *testing.T) {
	// SEQUENCE { INTEGER 42 }
	der := []byte{0x30, 0x03, 0x02, 0x01, 0x2a}
	result, err := berToDER(der)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, der) {
		t.Errorf("expected %x, got %x", der, result)
	}
}

// TestBerToDER_IndefiniteLength verifies conversion of indefinite-length
// constructed elements to definite-length DER.
func TestBerToDER_IndefiniteLength(t *testing.T) {
	// BER: SEQUENCE (indefinite) { INTEGER 42 } end-of-contents
	// 0x30 0x80 = SEQUENCE, indefinite length
	// 0x02 0x01 0x2a = INTEGER 42
	// 0x00 0x00 = end-of-contents
	ber := []byte{0x30, 0x80, 0x02, 0x01, 0x2a, 0x00, 0x00}

	// Expected DER: SEQUENCE (definite, length=3) { INTEGER 42 }
	expected := []byte{0x30, 0x03, 0x02, 0x01, 0x2a}

	result, err := berToDER(ber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, expected) {
		t.Errorf("expected %x, got %x", expected, result)
	}
}

// TestBerToDER_NestedIndefinite verifies handling of nested indefinite-length elements.
func TestBerToDER_NestedIndefinite(t *testing.T) {
	// Outer SEQUENCE (indefinite) {
	//   Inner SEQUENCE (indefinite) {
	//     INTEGER 1
	//   } end-of-contents
	//   INTEGER 2
	// } end-of-contents
	ber := []byte{
		0x30, 0x80, // outer SEQUENCE indefinite
		0x30, 0x80, // inner SEQUENCE indefinite
		0x02, 0x01, 0x01, // INTEGER 1
		0x00, 0x00, // inner end-of-contents
		0x02, 0x01, 0x02, // INTEGER 2
		0x00, 0x00, // outer end-of-contents
	}

	result, err := berToDER(ber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it's valid DER by parsing with encoding/asn1
	var outer asn1.RawValue
	rest, err := asn1.Unmarshal(result, &outer)
	if err != nil {
		t.Fatalf("result is not valid DER: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("unexpected trailing data: %x", rest)
	}
	if outer.Tag != 16 || outer.Class != 0 || !outer.IsCompound {
		t.Errorf("expected SEQUENCE, got tag=%d class=%d compound=%v", outer.Tag, outer.Class, outer.IsCompound)
	}
}

// TestBerToDER_LongFormLength verifies elements with long-form definite lengths
// pass through correctly.
func TestBerToDER_LongFormLength(t *testing.T) {
	// OCTET STRING with 200 bytes of content (long-form length: 0x81 0xc8)
	content := make([]byte, 200)
	for i := range content {
		content[i] = byte(i)
	}
	der := append([]byte{0x04, 0x81, 0xc8}, content...)

	result, err := berToDER(der)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, der) {
		t.Errorf("long-form length not preserved correctly")
	}
}

// TestBerToDER_MultiByteTag verifies elements with multi-byte tags are handled.
func TestBerToDER_MultiByteTag(t *testing.T) {
	// Context-specific [31] (multi-byte tag: 0x9f 0x1f), primitive, length 1, value 0x05
	ber := []byte{0x9f, 0x1f, 0x01, 0x05}
	result, err := berToDER(ber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, ber) {
		t.Errorf("expected %x, got %x", ber, result)
	}
}

// TestBerToDER_MixedDefiniteAndIndefinite verifies a structure with both
// definite and indefinite-length elements.
func TestBerToDER_MixedDefiniteAndIndefinite(t *testing.T) {
	// SEQUENCE (indefinite) {
	//   OCTET STRING (definite, 3 bytes) "abc"
	//   SEQUENCE (indefinite) {
	//     BOOLEAN TRUE
	//   } end-of-contents
	// } end-of-contents
	ber := []byte{
		0x30, 0x80, // SEQUENCE indefinite
		0x04, 0x03, 0x61, 0x62, 0x63, // OCTET STRING "abc"
		0x30, 0x80, // inner SEQUENCE indefinite
		0x01, 0x01, 0xff, // BOOLEAN TRUE
		0x00, 0x00, // inner end-of-contents
		0x00, 0x00, // outer end-of-contents
	}

	result, err := berToDER(ber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify round-trip through encoding/asn1
	var outer asn1.RawValue
	rest, err := asn1.Unmarshal(result, &outer)
	if err != nil {
		t.Fatalf("result is not valid DER: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("unexpected trailing data: %x", rest)
	}

	// Parse inner children
	inner := outer.Bytes
	var octetString asn1.RawValue
	inner, err = asn1.Unmarshal(inner, &octetString)
	if err != nil {
		t.Fatalf("failed to parse OCTET STRING: %v", err)
	}
	if string(octetString.Bytes) != "abc" {
		t.Errorf("expected 'abc', got %q", string(octetString.Bytes))
	}

	var innerSeq asn1.RawValue
	_, err = asn1.Unmarshal(inner, &innerSeq)
	if err != nil {
		t.Fatalf("failed to parse inner SEQUENCE: %v", err)
	}
}

// TestBerToDER_EmptySequence verifies indefinite-length empty sequences.
func TestBerToDER_EmptySequence(t *testing.T) {
	// SEQUENCE (indefinite) { } end-of-contents
	ber := []byte{0x30, 0x80, 0x00, 0x00}
	expected := []byte{0x30, 0x00}

	result, err := berToDER(ber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, expected) {
		t.Errorf("expected %x, got %x", expected, result)
	}
}

// TestBerToDER_Truncated verifies error handling for truncated data.
func TestBerToDER_Truncated(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"tag only", []byte{0x02}},
		{"indefinite no eoc", []byte{0x30, 0x80, 0x02, 0x01, 0x01}},
		{"content beyond data", []byte{0x04, 0x05, 0x01}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := berToDER(tc.data)
			if err == nil {
				t.Error("expected error for truncated data")
			}
		})
	}
}

// TestBerToDER_IndefiniteOnPrimitive verifies that indefinite length on a
// primitive element returns an error (invalid BER).
func TestBerToDER_IndefiniteOnPrimitive(t *testing.T) {
	// INTEGER (primitive) with indefinite length — invalid
	ber := []byte{0x02, 0x80, 0x01, 0x00, 0x00}
	_, err := berToDER(ber)
	if err == nil {
		t.Error("expected error for indefinite length on primitive element")
	}
}
