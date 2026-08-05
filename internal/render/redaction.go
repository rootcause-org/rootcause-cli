package render

// Run detail (events, prompts, grounding, commands, stdout/stderr) is served only to project-level
// admins. A redacted response still 200s with the normal envelope, so an unaware renderer would show an
// empty trace / a pattern-free fleet — a FALSE clean bill of health. Every surface that can receive a
// redacted payload prints one of these instead of the silently-empty section.
const (
	// RedactedTraceNotice: one run's trace/events came back without its detail.
	RedactedTraceNotice = "Trace detail withheld — project-level admin required."

	// RedactedFeedNotice: a bulk feed's rows kept their skeleton but lost reasoning/output/commands.
	RedactedFeedNotice = "run detail withheld — project-level admin required for reasoning, output, and commands"
)
