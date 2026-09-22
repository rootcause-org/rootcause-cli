// Transcript: the READING view of a chat conversation, folded out of one thread trace plus one
// /trace header per run. It exists because the raw path (`rc run thread` + one `rc run trace
// --raw-output` per turn) hands a reader ~270 KB for ~10 KB of conversation: the system prompt,
// the prompt sections and two identical tenant-settings blobs dwarf the question and the answer.
//
// Pure functions over wire rows (no I/O, no clock) — the CLI does the fetching, render draws it.
package digest

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/rootcause-org/rootcause-cli/internal/client"
)

// formAnswerPrefix marks a clarification-FORM submission rather than a typed question: the chat plane
// lowers a filled-in form to a user message `User selected: key=value; …` (server internal/chat/lower.go).
// Such a turn answers the PREVIOUS assistant turn, so a transcript folds it under that turn and does not
// count it as a question of its own.
const formAnswerPrefix = "User selected:"

// TranscriptFeedback is the human rating on one turn, as the thread trace's attribution ships it.
type TranscriptFeedback struct {
	Score   *int16 `json:"score,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// TranscriptTurn is one run of the conversation, in reading order.
//
// Seq numbers QUESTIONS, not rows: a folded form answer carries the Seq of the turn it answers, and
// IsFormAnswer tells the two apart. Unavailable is set (instead of failing the whole command) when the
// per-run header could not be fetched — a partial transcript beats no transcript.
type TranscriptTurn struct {
	Seq          int                 `json:"seq"`
	RunID        string              `json:"run_id"`
	CreatedAt    string              `json:"created_at"`
	PrincipalID  string              `json:"principal_id,omitempty"`
	Question     string              `json:"question,omitempty"`
	Answer       string              `json:"answer,omitempty"`
	Outcome      string              `json:"outcome,omitempty"`
	Feedback     *TranscriptFeedback `json:"feedback,omitempty"`
	RunURL       string              `json:"run_url,omitempty"`
	IsFormAnswer bool                `json:"is_form_answer"`
	Unavailable  string              `json:"unavailable,omitempty"`
}

// Transcript is the whole conversation: the turns in order plus the honest counts a reader needs to
// know what they are looking at.
type Transcript struct {
	SessionID        string           `json:"session_id"`
	Turns            []TranscriptTurn `json:"turns"`
	Questions        int              `json:"questions"`
	FormAnswers      int              `json:"form_answers"`
	AnswersTruncated bool             `json:"answers_truncated,omitempty"`
}

// TranscriptSource is one run's fetched /trace header (or the reason it is missing). The CLI fills
// these; the builder stays I/O-free.
type TranscriptSource struct {
	Trace *client.FullResponse
	Err   string
}

// BuildTranscript folds a thread trace's runs (any order) + their trace headers into reading order.
// Ordering is by created_at, run id as the tie-break, so the output is deterministic even when two
// runs share a timestamp.
func BuildTranscript(sessionID string, runs []client.RunSummary, sources map[string]TranscriptSource) Transcript {
	ordered := append([]client.RunSummary(nil), runs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt != ordered[j].CreatedAt {
			return ordered[i].CreatedAt < ordered[j].CreatedAt
		}
		return ordered[i].RunID < ordered[j].RunID
	})

	out := Transcript{SessionID: sessionID, Turns: make([]TranscriptTurn, 0, len(ordered))}
	seq := 0
	for _, r := range ordered {
		turn := TranscriptTurn{RunID: r.RunID, CreatedAt: r.CreatedAt, Feedback: attributionFeedback(r)}
		src := sources[r.RunID]
		switch {
		case src.Trace != nil:
			h := &src.Trace.Run
			turn.Question = strings.TrimSpace(h.Question)
			turn.Answer = transcriptAnswer(h)
			turn.Outcome = metaText(h.Metadata, "outcome")
			// The run page link without its ?t= share token: the token is an account-less credential and a
			// transcript is exactly the text people paste into tickets and chats.
			turn.RunURL = stripQuery(metaText(h.Metadata, "run_url"))
			turn.PrincipalID = PrincipalID(h.Guards)
			if src.Trace.Redacted() && turn.Answer == "" {
				turn.Unavailable = "trace detail withheld from this token"
			}
		case src.Err != "":
			turn.Unavailable = src.Err
		default:
			turn.Unavailable = "no trace fetched for this run"
		}
		// Fall back to the index-level facts when the header has none, so a turn never reads empty just
		// because the trace was thin.
		if turn.Outcome == "" {
			turn.Outcome = r.Outcome
		}
		turn.IsFormAnswer = len(out.Turns) > 0 && IsFormAnswer(turn.Question)
		if turn.IsFormAnswer {
			out.FormAnswers++
			turn.Seq = seq
		} else {
			seq++
			out.Questions++
			turn.Seq = seq
		}
		out.Turns = append(out.Turns, turn)
	}
	return out
}

// IsFormAnswer reports whether a question body is a clarification-form submission: its first non-empty
// line starts with the lowered "User selected:" marker. A turn where the user ALSO typed prose first is
// a real question and stays one.
func IsFormAnswer(question string) bool {
	for _, line := range strings.Split(question, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, formAnswerPrefix)
	}
	return false
}

// TranscriptAnswerHeadChars / TranscriptPagingThreshold make the transcript honest about size: past the
// threshold the command truncates each answer to a head and SAYS so, rather than silently dumping a
// hundred full answers into a terminal.
const (
	TranscriptPagingThreshold = 40
	TranscriptAnswerHeadChars = 800
)

// TruncateAnswers clips every answer to a head and marks the transcript truncated. The CLI calls it
// only for long conversations; the per-turn detail stays one `rc run trace <run> --brief` away.
func (t *Transcript) TruncateAnswers(head int) {
	if head <= 0 {
		return
	}
	for i := range t.Turns {
		a := t.Turns[i].Answer
		if r := []rune(a); len(r) > head {
			t.Turns[i].Answer = strings.TrimRight(string(r[:head]), " \n") + " […]"
			t.AnswersTruncated = true
		}
	}
}

// transcriptAnswer is what the turn replied with: the draft body, the raw-scenario answer, or — when
// the agent deliberately placed nothing — its own decline words, so an empty answer is never silent.
func transcriptAnswer(h *client.RunHeader) string {
	for _, body := range []string{h.Draft, h.DraftMarkdown, h.AnswerMarkdown} {
		if s := strings.TrimSpace(body); s != "" {
			return s
		}
	}
	for _, why := range []string{h.Decline, h.DeclineReason} {
		if s := strings.TrimSpace(why); s != "" {
			return "(declined) " + s
		}
	}
	return ""
}

func attributionFeedback(r client.RunSummary) *TranscriptFeedback {
	if r.Attribution == nil || r.Attribution.Feedback == nil {
		return nil
	}
	f := r.Attribution.Feedback
	if f.Score == nil && strings.TrimSpace(f.Comment) == "" {
		return nil
	}
	return &TranscriptFeedback{Score: f.Score, Comment: strings.TrimSpace(f.Comment)}
}

// PrincipalID names WHO held this turn, from the run's principal scope: the admin id the app asserted,
// else the external id (claims first, then the scope's own field). "" when the run carried no principal
// scope, or the caller's tier does not get guards at all.
func PrincipalID(g *client.GuardsView) string {
	if g == nil || len(g.PrincipalScope) == 0 {
		return ""
	}
	var scope struct {
		ExternalID string                     `json:"external_id"`
		Claims     map[string]json.RawMessage `json:"claims"`
	}
	if err := json.Unmarshal(g.PrincipalScope, &scope); err != nil {
		return ""
	}
	for _, key := range []string{"admin_id", "external_id"} {
		if v := jsonScalar(scope.Claims[key]); v != "" {
			return v
		}
	}
	return strings.TrimSpace(scope.ExternalID)
}

// jsonScalar renders a claim that may be a string OR a number (an admin id is often an integer) without
// turning it into Go syntax.
func jsonScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var f json.Number
	if json.Unmarshal(raw, &f) == nil {
		return f.String()
	}
	return ""
}

// metaText reads a string value out of the run header's freeform metadata map.
func metaText(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

func stripQuery(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}
