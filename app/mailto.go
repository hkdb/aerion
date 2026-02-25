package app

import (
	"net/url"
	"strings"
)

// ParseMailtoURL parses a mailto: URL and extracts email data
// Format: mailto:addr1,addr2?subject=...&body=...&cc=...&bcc=...
func ParseMailtoURL(rawURL string) *MailtoData {
	if !strings.HasPrefix(strings.ToLower(rawURL), "mailto:") {
		return nil
	}

	data := &MailtoData{}

	// Remove mailto: prefix
	rest := rawURL[7:]

	// Split into address part and query part
	queryStart := strings.Index(rest, "?")
	var addrPart, queryPart string
	if queryStart == -1 {
		addrPart = rest
	} else {
		addrPart = rest[:queryStart]
		queryPart = rest[queryStart+1:]
	}

	// Parse To addresses (comma-separated, URL-encoded)
	if addrPart != "" {
		decoded, err := url.QueryUnescape(addrPart)
		if err == nil {
			addrPart = decoded
		}
		// Split by comma and trim whitespace
		for _, addr := range strings.Split(addrPart, ",") {
			addr = strings.TrimSpace(addr)
			if addr != "" {
				data.To = append(data.To, addr)
			}
		}
	}

	// Parse query parameters
	if queryPart != "" {
		params, err := url.ParseQuery(queryPart)
		if err == nil {
			if subject := params.Get("subject"); subject != "" {
				data.Subject = subject
			}
			if body := params.Get("body"); body != "" {
				data.Body = body
			}
			if cc := params.Get("cc"); cc != "" {
				for _, addr := range strings.Split(cc, ",") {
					addr = strings.TrimSpace(addr)
					if addr != "" {
						data.Cc = append(data.Cc, addr)
					}
				}
			}
			if bcc := params.Get("bcc"); bcc != "" {
				for _, addr := range strings.Split(bcc, ",") {
					addr = strings.TrimSpace(addr)
					if addr != "" {
						data.Bcc = append(data.Bcc, addr)
					}
				}
			}
		}
	}

	return data
}
