package cli

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rootcause-org/rootcause-cli/internal/client"
	"github.com/rootcause-org/rootcause-cli/internal/digest"
	"github.com/rootcause-org/rootcause-cli/internal/render"
)

// transcriptFetchers bounds the fan-out over one conversation's runs. The thread trace names N runs and
// each needs its own /trace header; 4 in flight keeps a 30-turn conversation to a few seconds without
// hammering the API for what is, to the server, an ordinary read burst.
const transcriptFetchers = 4

// runThreadTranscript is the `rc run thread <session> --transcript` path: ONE command that prints the
// conversation the way a human reads it. The default `rc run thread` view answers "what happened to the
// pipeline"; this one answers "what was asked and what did we answer", which previously cost one
// `rc run trace --raw-output` per turn (megabytes of system prompt for kilobytes of conversation).
func runThreadTranscript(e *env, target shareTarget) error {
	if target.shared() {
		// A share link is scoped to ONE run, so it can never enumerate a conversation's other turns.
		return fmt.Errorf("--transcript needs a login: a share link carries one run, not the whole conversation")
	}
	c, err := e.newClient()
	if err != nil {
		return err
	}
	lookupID, err := resolveThreadLookupID(e, c, target.runID)
	if err != nil {
		return withRunAccessHint(err)
	}
	tr, _, err := c.ThreadTrace(e.ctx(), lookupID, e.scopeProject(), e.scopeTenant())
	if err != nil {
		return withRunAccessHint(err)
	}
	sources, err := fetchTranscriptSources(e, c, tr.Runs)
	if err != nil {
		return withRunAccessHint(err)
	}
	doc := digest.BuildTranscript(transcriptSessionID(tr), tr.Runs, sources)
	if len(doc.Turns) > digest.TranscriptPagingThreshold {
		doc.TruncateAnswers(digest.TranscriptAnswerHeadChars)
	}
	// The Markdown digest IS the deliverable of --transcript, and agents always run without a TTY, so
	// auto-mode must not fall back to JSON here (it did: every non-terminal caller got JSON). JSON only
	// on an explicit -o json.
	if e.mode() == render.ModeJSON {
		body, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		return e.renderJSON("thread-transcript-"+shortRunID(lookupID), body)
	}
	render.Transcript(e.out, doc)
	if len(doc.Turns) == 0 {
		_, _ = fmt.Fprintf(e.err, "hint: %s\n", runShareHint)
	}
	return nil
}

// fetchTranscriptSources fetches every run's /trace header with a bounded worker pool. A single run's
// failure is recorded on its turn rather than failing the read — a partial transcript is still the
// answer to "what was said". It errors only when NOTHING could be fetched (auth/scope/network), which a
// per-turn note would misrepresent as an empty conversation.
func fetchTranscriptSources(e *env, c *client.Client, runs []client.RunSummary) (map[string]digest.TranscriptSource, error) {
	sources := make(map[string]digest.TranscriptSource, len(runs))
	if len(runs) == 0 {
		return sources, nil
	}
	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		firstErr  error
		okCount   int
		semaphore = make(chan struct{}, transcriptFetchers)
	)
	for _, r := range runs {
		wg.Add(1)
		go func(runID string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			trace, _, err := c.FullWithRaw(e.ctx(), runID, e.scopeProject(), e.scopeTenant())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				sources[runID] = digest.TranscriptSource{Err: err.Error()}
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			okCount++
			sources[runID] = digest.TranscriptSource{Trace: trace}
		}(r.RunID)
	}
	wg.Wait()
	if okCount == 0 && firstErr != nil {
		return nil, firstErr
	}
	return sources, nil
}

// transcriptSessionID prefers the session id the runs carry (the conversation's own identity) over the
// id the caller happened to paste, which may be a run or a provider thread id.
func transcriptSessionID(tr *client.ThreadTrace) string {
	for _, r := range tr.Runs {
		if r.SessionID != "" {
			return r.SessionID
		}
	}
	return tr.ID
}
