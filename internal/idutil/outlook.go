package idutil

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// OutlookResult classifies an Outlook / Microsoft Graph id and tells you which
// DB column (if any) it can be matched against offline.
//
// owa-web ids (the id in the web URL) are NOT offline-resolvable — MatchColumn
// is empty; resolve them live via Graph POST /me/translateExchangeIds.
type OutlookResult struct {
	Input        string `json:"input"`
	URLID        string `json:"url_id"` // the extracted id when input was a URL
	Kind         string `json:"kind"`   // owa-web|graph-message|conversation|unknown
	MatchColumn  string `json:"match_column"`
	MatchValue   string `json:"match_value"`
	Note         string `json:"note"`
	EmbeddedGUID string `json:"embedded_guid"`
}

// EWS/OWA store-entry ids begin with the id-version + id-storage-type bytes
// 0x01 0x04. This 2-byte prefix reliably distinguishes an OWA/EWS message
// EntryID from a Graph REST/conversation id.
var ewsPrefix = []byte{0x01, 0x04}

var (
	reOutlookURLID = regexp.MustCompile(`/id/([^/?#&]+)`)
	reOutlookAAMk  = regexp.MustCompile(`^AAMk`)
	reOutlookB64   = regexp.MustCompile(`^[A-Za-z0-9+/_=-]+$`)
)

// ClassifyOutlook identifies an Outlook web URL, OWA id, Graph REST message id,
// or conversationId, and maps it to the DB column it's queryable by (if any).
func ClassifyOutlook(token string) OutlookResult {
	res := OutlookResult{Input: token}

	extracted := extractOutlookURL(token)
	if extracted != token {
		res.URLID = extracted
	}
	t := strings.TrimSpace(extracted)

	raw := b64Any(t)

	// 1) OWA/EWS message store-entry id (the web-URL id). 0x01 0x04 prefix.
	if len(raw) >= 2 && raw[0] == ewsPrefix[0] && raw[1] == ewsPrefix[1] {
		guid := embeddedGUID(raw)
		res.Kind = "owa-web"
		res.EmbeddedGUID = guid
		note := "OWA/EWS message EntryID (from the web URL). NOT a conversationId and " +
			"NOT offline-convertible to one — translate live via Graph " +
			"POST /me/translateExchangeIds, or use the recent-threads fallback " +
			"(match by subject/sender/time)."
		if guid != "" {
			note += "  embedded message GUID: " + guid
		}
		res.Note = note
		return res
	}

	// 2) Graph REST message id: long, starts AAMk → messages.external_message_id.
	if reOutlookAAMk.MatchString(t) && len(t) >= 100 {
		res.Kind = "graph-message"
		res.MatchColumn = "messages.external_message_id"
		res.MatchValue = t
		res.Note = "Graph REST message id — match messages.external_message_id, then its thread_id."
		return res
	}

	// 3) conversationId: base64, len>=16, typically AAQk → threads.external_thread_id.
	if len(raw) > 0 && len(t) >= 16 && reOutlookB64.MatchString(t) {
		res.Kind = "conversation"
		res.MatchColumn = "threads.external_thread_id"
		res.MatchValue = t
		res.Note = "Looks like a Graph conversationId — match threads.external_thread_id."
		return res
	}

	res.Kind = "unknown"
	res.Note = "unrecognized Outlook id shape"
	return res
}

// extractOutlookURL pulls the /id/<ID> segment out of an Outlook web URL and
// URL-decodes it (handles %2B/%2F encoding). Returns token unchanged otherwise.
func extractOutlookURL(token string) string {
	if !strings.Contains(token, "outlook.") && !strings.Contains(token, "/id/") {
		return token
	}
	raw := token
	if m := reOutlookURLID.FindStringSubmatch(token); m != nil {
		raw = m[1]
	}
	if dec, err := url.QueryUnescape(raw); err == nil {
		return dec
	}
	return raw
}

// b64Any decodes a base64 / base64url string regardless of alphabet or padding.
// Returns nil if it isn't valid base64 at all.
func b64Any(s string) []byte {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return raw
}

// embeddedGUID pulls the trailing 16-byte GUID out of an EWS EntryID. Purely
// informational. The first three groups are little-endian (Exchange convention).
func embeddedGUID(raw []byte) string {
	if len(raw) < 16 {
		return ""
	}
	g := raw[len(raw)-16:]
	d1 := binary.LittleEndian.Uint32(g[0:4])
	d2 := binary.LittleEndian.Uint16(g[4:6])
	d3 := binary.LittleEndian.Uint16(g[6:8])
	d4 := fmt.Sprintf("%x", g[8:10])
	d5 := fmt.Sprintf("%x", g[10:16])
	return fmt.Sprintf("%08x-%04x-%04x-%s-%s", d1, d2, d3, d4, d5)
}
