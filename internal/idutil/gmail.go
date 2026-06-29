// Package idutil translates between provider-native thread/message id formats
// (Gmail, Outlook/Graph) and the keys we store in the DB. Pure functions, no I/O.
package idutil

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
)

// GmailResult is the structured classification of a Gmail id token.
// Hex is empty and Decimal is 0 when the id is not base-convertible
// (client-local drafts, opaque web blobs, unknown shapes).
type GmailResult struct {
	Input   string `json:"input"`
	Kind    string `json:"kind"` // legacy-f|local|web-opaque|decimal|hex|unknown
	Hex     string `json:"hex"`
	Decimal uint64 `json:"decimal"`
	ThreadF string `json:"thread_f"`
	MsgF    string `json:"msg_f"`
	Note    string `json:"note"`
	WebURL  string `json:"web_url"`
}

var (
	reGmailURLFragment = regexp.MustCompile(`#[^/]+/([A-Za-z0-9_-]+)`)
	reGmailLegacyF     = regexp.MustCompile(`^(?:thread|msg)-f:(\d+)$`)
	reGmailLocalA      = regexp.MustCompile(`^(?:thread|msg)-a:.+$`)
	reGmailDraftR      = regexp.MustCompile(`^r\d{6,}$`)
	reGmailB64url      = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)
	reGmailHex16       = regexp.MustCompile(`^[0-9a-fA-F]{16}$`)
	reGmailDecimal     = regexp.MustCompile(`^\d{15,20}$`)
)

// ClassifyGmail identifies a Gmail thread/message id token and, where the id is
// the IMAP-derived 64-bit value, derives its hex/decimal/legacy-f forms + web URL.
func ClassifyGmail(token string) GmailResult {
	return classifyGmailUser(token, "0")
}

// ClassifyGmailForUser is like ClassifyGmail but builds the web URL for the
// given Gmail /u/N/ mailbox index.
func ClassifyGmailForUser(token, user string) GmailResult {
	return classifyGmailUser(token, user)
}

func classifyGmailUser(token, user string) GmailResult {
	res := GmailResult{Input: token}
	t := strings.TrimSpace(token)

	// Full URL? pull the id after the last '#folder/' segment.
	if m := reGmailURLFragment.FindStringSubmatch(t); m != nil {
		t = m[1]
	}

	// Legacy prefixed forms: thread-f:<dec> / msg-f:<dec> — decimal of the hex id.
	if m := reGmailLegacyF.FindStringSubmatch(t); m != nil {
		dec, _ := strconv.ParseUint(m[1], 10, 64)
		res.Kind = "legacy-f"
		fillGmailHex(&res, dec, user)
		return res
	}

	// Client-local (drafts / all-mail) — not hex-derived.
	if reGmailLocalA.MatchString(t) || reGmailDraftR.MatchString(t) {
		res.Kind = "local"
		res.Note = "client-local / draft id — no hex equivalent"
		return res
	}

	// Opaque web blob: long base64url, mixed case, NOT a clean 16-hex id.
	if reGmailB64url.MatchString(t) && !reGmailHex16.MatchString(t) {
		raw := decodeURLSafe(t)
		res.Kind = "web-opaque"
		res.Note = "opaque " + strconv.Itoa(len(raw)) + "-byte server token — not offline-convertible"
		return res
	}

	// Bare decimal (the 64-bit int).
	if reGmailDecimal.MatchString(t) {
		dec, _ := strconv.ParseUint(t, 10, 64)
		res.Kind = "decimal"
		fillGmailHex(&res, dec, user)
		return res
	}

	// Hex id.
	if reGmailHex16.MatchString(t) {
		dec, _ := strconv.ParseUint(strings.ToLower(t), 16, 64)
		res.Kind = "hex"
		fillGmailHex(&res, dec, user)
		return res
	}

	res.Kind = "unknown"
	res.Note = "unrecognized id format"
	return res
}

// fillGmailHex populates the hex/decimal/legacy + URL fields from the 64-bit id.
func fillGmailHex(res *GmailResult, dec uint64, user string) {
	hex := strconv.FormatUint(dec, 16)
	res.Hex = hex
	res.Decimal = dec
	res.ThreadF = "thread-f:" + strconv.FormatUint(dec, 10)
	res.MsgF = "msg-f:" + strconv.FormatUint(dec, 10)
	res.WebURL = GmailWebURL(hex, user)
}

// GmailWebURL builds a clickable thread URL. Gmail resolves a legacy hex id
// dropped into the URL hash, so #all/<hex> opens the conversation.
func GmailWebURL(hexid, user string) string {
	return "https://mail.google.com/mail/u/" + user + "/#all/" + hexid
}

// decodeURLSafe decodes a base64url token, padding as needed; b” on failure.
func decodeURLSafe(s string) []byte {
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return raw
}
