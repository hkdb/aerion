package smime

import (
	"encoding/binary"
	"fmt"
)

// berToDER converts BER-encoded ASN.1 data to DER by resolving
// indefinite-length constructs. Returns the data unchanged if it's
// already valid DER.
func berToDER(data []byte) ([]byte, error) {
	result, _, err := convertElement(data, 0)
	if err != nil {
		return nil, fmt.Errorf("BER to DER conversion failed: %w", err)
	}
	return result, nil
}

// convertElement reads one ASN.1 element at offset and returns it in DER form.
func convertElement(data []byte, offset int) ([]byte, int, error) {
	if offset >= len(data) {
		return nil, offset, fmt.Errorf("unexpected end of data")
	}

	// Read tag
	tagStart := offset
	tag := data[offset]
	offset++

	// Handle multi-byte tags (tag number >= 31)
	if tag&0x1f == 0x1f {
		for offset < len(data) && data[offset]&0x80 != 0 {
			offset++
		}
		if offset >= len(data) {
			return nil, offset, fmt.Errorf("truncated tag")
		}
		offset++ // final tag byte
	}

	if offset >= len(data) {
		return nil, offset, fmt.Errorf("truncated element")
	}

	tagBytes := data[tagStart:offset]
	isConstructed := tag&0x20 != 0

	// Read length
	lengthByte := data[offset]
	offset++

	// Indefinite length (BER only, not valid DER)
	if lengthByte == 0x80 {
		if !isConstructed {
			return nil, offset, fmt.Errorf("indefinite length on primitive element")
		}
		return convertIndefinite(data, offset, tagBytes)
	}

	// Definite short form
	if lengthByte&0x80 == 0 {
		contentLen := int(lengthByte)
		return convertDefinite(data, offset, contentLen, tagBytes, isConstructed)
	}

	// Definite long form
	numBytes := int(lengthByte & 0x7f)
	if numBytes > 4 || offset+numBytes > len(data) {
		return nil, offset, fmt.Errorf("invalid length encoding")
	}
	contentLen := 0
	for i := 0; i < numBytes; i++ {
		contentLen = contentLen<<8 | int(data[offset])
		offset++
	}
	return convertDefinite(data, offset, contentLen, tagBytes, isConstructed)
}

// convertDefinite handles a definite-length element, recursively converting
// children if constructed.
func convertDefinite(data []byte, offset, contentLen int, tagBytes []byte, isConstructed bool) ([]byte, int, error) {
	if offset+contentLen > len(data) {
		return nil, offset, fmt.Errorf("content extends beyond data")
	}

	content := data[offset : offset+contentLen]
	offset += contentLen

	if !isConstructed {
		return buildDERElement(tagBytes, content), offset, nil
	}

	// Constructed: recursively convert children
	converted, err := convertChildren(content)
	if err != nil {
		return nil, offset, err
	}
	return buildDERElement(tagBytes, converted), offset, nil
}

// convertIndefinite reads children until end-of-contents (0x00 0x00),
// converts them to DER, and returns the element with definite length.
func convertIndefinite(data []byte, offset int, tagBytes []byte) ([]byte, int, error) {
	var children []byte

	for {
		if offset+1 >= len(data) {
			return nil, offset, fmt.Errorf("unterminated indefinite length")
		}
		// Check for end-of-contents octets
		if data[offset] == 0x00 && data[offset+1] == 0x00 {
			offset += 2
			break
		}
		child, newOffset, err := convertElement(data, offset)
		if err != nil {
			return nil, offset, err
		}
		children = append(children, child...)
		offset = newOffset
	}

	return buildDERElement(tagBytes, children), offset, nil
}

// convertChildren recursively converts all children in a constructed element.
func convertChildren(data []byte) ([]byte, error) {
	var result []byte
	offset := 0
	for offset < len(data) {
		child, newOffset, err := convertElement(data, offset)
		if err != nil {
			return nil, err
		}
		result = append(result, child...)
		offset = newOffset
	}
	return result, nil
}

// buildDERElement creates a DER-encoded element from tag and content bytes.
func buildDERElement(tag, content []byte) []byte {
	length := len(content)
	var element []byte
	element = append(element, tag...)

	switch {
	case length < 0x80:
		element = append(element, byte(length))
	case length < 0x100:
		element = append(element, 0x81, byte(length))
	case length < 0x10000:
		element = append(element, 0x82)
		element = binary.BigEndian.AppendUint16(element, uint16(length))
	case length < 0x1000000:
		element = append(element, 0x83, byte(length>>16), byte(length>>8), byte(length))
	default:
		element = append(element, 0x84)
		element = binary.BigEndian.AppendUint32(element, uint32(length))
	}

	element = append(element, content...)
	return element
}
