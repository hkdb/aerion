// Package unsubscribe detects and performs mailing-list unsubscribe actions.
//
// Detection follows the same strategy as Mailspring's unsubscribe feature:
//
//  1. The List-Unsubscribe header (RFC 2369), which may carry mailto: and/or
//     http(s): URIs.
//  2. The List-Unsubscribe-Post header (RFC 8058) which, together with an
//     https URI, enables a silent "one-click" POST.
//  3. As a fallback, an "unsubscribe" link found in the HTML body.
//
// Preferred action order: one-click POST > mailto (sent silently) > open the
// http(s) URI in the browser > open the body link in the browser.
package unsubscribe

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// Method identifies how an unsubscribe request will be performed.
type Method string

const (
	// MethodNone means the message offers no unsubscribe mechanism.
	MethodNone Method = ""
	// MethodOneClick is an RFC 8058 one-click HTTPS POST.
	MethodOneClick Method = "one-click"
	// MethodMailto sends an unsubscribe e-mail from the receiving account.
	MethodMailto Method = "mailto"
	// MethodBrowser opens an unsubscribe page in the system browser.
	MethodBrowser Method = "browser"
)

// Info describes the unsubscribe options of a message.
type Info struct {
	// Available is true when at least one unsubscribe mechanism exists.
	Available bool `json:"available"`
	// Method is the mechanism that Unsubscribe will try first.
	Method Method `json:"method"`
	// Target is a human-readable target for the preferred method
	// (the mailto address or the URL host) shown in the confirmation dialog.
	Target string `json:"target"`
	// FromBody is true when the only mechanism is a link found in the body
	// (less reliable than the List-Unsubscribe header).
	FromBody bool `json:"fromBody"`

	// Raw options, used by the backend to perform/fall back. Not needed by the UI.
	HTTPURLs  []string `json:"-"`
	MailtoURL string   `json:"-"`
	OneClick  bool     `json:"-"`
	BodyURL   string   `json:"-"`
}

// maxURILength guards against absurd header values.
const maxURILength = 2048

// ParseHeaders parses the List-Unsubscribe and List-Unsubscribe-Post header
// values. It returns the http(s) URIs and the first mailto: URI found, and
// whether one-click POST is advertised.
func ParseHeaders(listUnsubscribe, listUnsubscribePost string) (httpURLs []string, mailto string, oneClick bool) {
	// Header values may be folded; remove line breaks before parsing.
	value := strings.NewReplacer("\r", "", "\n", "").Replace(listUnsubscribe)

	// RFC 2369: one or more URIs enclosed in angle brackets, comma-separated.
	for {
		start := strings.Index(value, "<")
		if start == -1 {
			break
		}
		end := strings.Index(value[start+1:], ">")
		if end == -1 {
			break
		}
		uri := strings.TrimSpace(value[start+1 : start+1+end])
		value = value[start+1+end+1:]

		// Some senders insert whitespace inside the brackets
		uri = strings.Join(strings.Fields(uri), "")
		if uri == "" || len(uri) > maxURILength {
			continue
		}

		lower := strings.ToLower(uri)
		switch {
		case strings.HasPrefix(lower, "mailto:"):
			if mailto == "" {
				mailto = uri
			}
		case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
			httpURLs = append(httpURLs, uri)
		}
	}

	post := strings.ToLower(strings.Join(strings.Fields(listUnsubscribePost), ""))
	oneClick = post == "list-unsubscribe=one-click"

	return httpURLs, mailto, oneClick
}

// FirstHTTPS returns the first https:// URI in the list, or "".
func FirstHTTPS(urls []string) string {
	for _, u := range urls {
		if strings.HasPrefix(strings.ToLower(u), "https://") {
			return u
		}
	}
	return ""
}

// Detect builds the unsubscribe Info for a message from its headers and,
// as a fallback, its HTML body.
func Detect(listUnsubscribe, listUnsubscribePost, bodyHTML string) *Info {
	httpURLs, mailto, oneClick := ParseHeaders(listUnsubscribe, listUnsubscribePost)

	info := &Info{
		HTTPURLs:  httpURLs,
		MailtoURL: mailto,
		// RFC 8058 requires an https URI for one-click
		OneClick: oneClick && FirstHTTPS(httpURLs) != "",
	}

	switch {
	case info.OneClick:
		info.Method = MethodOneClick
		info.Target = HostOf(FirstHTTPS(httpURLs))
	case mailto != "":
		info.Method = MethodMailto
		info.Target = mailtoAddress(mailto)
	case len(httpURLs) > 0:
		info.Method = MethodBrowser
		info.Target = HostOf(httpURLs[0])
	default:
		if link := FindBodyLink(bodyHTML); link != "" {
			info.Method = MethodBrowser
			info.Target = HostOf(link)
			info.BodyURL = link
			info.FromBody = true
		}
	}

	info.Available = info.Method != MethodNone
	return info
}

// unsubscribeTextRe matches "unsubscribe" wording in the most common
// languages. Used against link text / title.
var unsubscribeTextRe = regexp.MustCompile(`(?i)(unsubscribe|un-subscribe|opt[\s-]?out|` +
	`disiscri|cancella(re)?\s+(l'|la\s+)?iscrizione|annulla(re)?\s+(l'|la\s+)?iscrizione|` +
	`abmelden|abbestellen|désinscri|desinscri|désabonne|desabonne|` +
	`darse\s+de\s+baja|darte\s+de\s+baja|cancelar\s+(la\s+)?suscripci|` +
	`cancelar\s+(a\s+)?inscri|descadastr|afmelden|uitschrijven|` +
	`wypisz|odhlásit|odhlasit|abonelikten\s+çık|avslutt|avmeld|` +
	`退订|取消订阅|取消訂閱|配信停止|登録解除)`)

// unsubscribeHrefRe matches unsubscribe-like URLs (weaker signal than text).
var unsubscribeHrefRe = regexp.MustCompile(`(?i)(unsubscribe|unsub|optout|opt-out|opt_out|disiscri|abmelden|desinscri|desabonne)`)

// FindBodyLink returns the most plausible http(s) unsubscribe link from an
// HTML body, or "" when none is found. Links whose visible text (or title)
// says "unsubscribe" win over links that only contain it in the URL.
func FindBodyLink(bodyHTML string) string {
	if bodyHTML == "" {
		return ""
	}

	doc, err := html.Parse(strings.NewReader(bodyHTML))
	if err != nil {
		return ""
	}

	var textMatch, hrefMatch string

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if textMatch != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			var href, title string
			for _, attr := range n.Attr {
				switch strings.ToLower(attr.Key) {
				case "href":
					href = strings.TrimSpace(attr.Val)
				case "title":
					title = attr.Val
				}
			}
			lower := strings.ToLower(href)
			if (strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) && len(href) <= maxURILength {
				text := nodeText(n)
				if unsubscribeTextRe.MatchString(text) || unsubscribeTextRe.MatchString(title) {
					textMatch = href
					return
				}
				if hrefMatch == "" && unsubscribeHrefRe.MatchString(href) {
					hrefMatch = href
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if textMatch != "" {
		return textMatch
	}
	return hrefMatch
}

// nodeText returns the concatenated text content of a node.
func nodeText(n *html.Node) string {
	var sb strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
			sb.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

// HostOf returns the host part of a URL for display purposes.
func HostOf(rawURL string) string {
	rest := rawURL
	if i := strings.Index(rest, "://"); i != -1 {
		rest = rest[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i != -1 {
		rest = rest[:i]
	}
	if i := strings.LastIndex(rest, "@"); i != -1 {
		rest = rest[i+1:]
	}
	return rest
}

// mailtoAddress returns the address part of a mailto: URI for display purposes.
func mailtoAddress(mailto string) string {
	rest := mailto[len("mailto:"):]
	if i := strings.Index(rest, "?"); i != -1 {
		rest = rest[:i]
	}
	return rest
}
