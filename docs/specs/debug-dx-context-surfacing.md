# rc debug DX: surface full context + context-export as first-class + fix rc-debug skill

Goal: when an agent debugs a production run by id, the rc CLI progressively discloses the whole
picture — assembled prompt with provenance, brain-plane text, draftcleanup rewrites — and the
rc-debug skill doc actually guides it there. Origin: 2026-08-13 operator session.

Repos: this one (`rootcause-cli`) + the skills kit (`rootcause-brain-skills`). Host-side data work
happens in a SIBLING thread in `rootcause-org/rootcause` (spec:
`docs/specs/prompt-provenance-observability.md` there) — it persists per-run: system-prompt section
map, full bootstrap/brain-plane user turn, draftcleanup events with before/after + cost lines, all
exposed via the `/full` dump API with 7-day retention.

## Locked decisions

1. **`rc run debug` renders the new planes** once the sibling's API fields exist: section map
   (id/gate/source per chunk), brain-plane text, draftcleanup pass timeline (before → pass →
   after, plus its cost line). Degrade gracefully when a run predates the fields or retention
   already purged them (say so explicitly, don't render empty sections silently).
   Sequencing: start with items 2–4 (independent); poll the sibling's `main` for the API shape.
2. **Promote `cmd/context-export` (host repo) to a first-class rc command.** Investigate the
   elegant shape — e.g. `rc dev context-export` wrapping/relocating the renderer — and pick;
   the requirement is: runnable from a brain checkout without knowing the host repo layout, and
   the run-dump markdown index gains a progressive-disclosure hint ("for the full three-step
   context with per-section sources, run: …"). If real code-sharing with the host is needed,
   coordinate with the sibling thread rather than forking the renderer.
3. **Fix the rc-debug skill in `rootcause-brain-skills`** — do whatever is elegant (operator's
   words). Known facts: `skills/rc-debug/SKILL.md:15-19` links `../../docs/*.md`; skill dirs are
   symlinked into brain checkouts (`brain/.agents/skills/rc-debug` →
   `~/.rootcause-brain-skills/skills/rc-debug`), so relative links resolve at the symlink target
   but break from the brain-checkout path agents actually use. Targets exist in the kit.
4. **Extend rc-debug SKILL.md content**: document the `type=="run"` dump header
   (`system_prompt`, `tenant_settings` vs `tenant_settings_current`, `brain_resolved`,
   `grounding_sources` jq one-liners — today it only documents event-space), state "the 35k
   system_prompt is only plane 1", and add a pointer stub to the host's
   `prompt-assembly-map.md` (canonical there; don't duplicate content into the kit).
   Respect the kit's release/versioning flow (see the brain-dev-upgrade skill) so installed kits
   pick the fix up.

## Out of scope

- `rc run prompt` subcommand (rejected — if the section map lands, rendering it inside
  `rc run debug` is enough), contradiction lint (rejected).

## Done criteria

- Kit fix released/published per the kit's own flow; verified from a real brain checkout (links
  resolve, new sections read correctly).
- CLI released per this repo's flow; proof = `rc run debug` on a fresh run showing the new
  sections rendered (or the graceful-absence message on an old run), and the context-export
  command running from `rootcause-brain-kampadmin` with the hint present in a fresh dump.
- One plane per commit where practical; other agents may be active on shared `main` — commit only
  your files, `git add` by explicit path.
