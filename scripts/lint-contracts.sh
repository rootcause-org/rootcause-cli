#!/usr/bin/env bash
#
# lint-contracts.sh — the layering contracts that no Go linter can express, as greps.
#
# WHY THIS EXISTS: `golangci-lint` (depguard/forbidigo) covers import- and symbol-shaped rules. The
# rules here are SHAPE-shaped — "no call of this form in this package" — and each encodes an
# architecture decision that regressed at least once before (docs/audits/architecture-2026-09-05.md,
# findings F2/F3, F13, F14):
#
#   cli-no-raw-write            internal/cli must not build its own path/verb: every request goes
#                               through a NAMED client method returning (typed, raw, error), so
#                               `-o json` and the table view can never diverge and the wire contract
#                               stays in internal/client. (F2/F3)
#   cli-no-render-funcs         table/report formatting belongs in internal/render, where it is a pure
#                               function of server rows and golden-testable. (F14)
#   cli-no-embedded-interpreter no Python/bash program shipped as a Go string literal — untestable,
#                               unformatted, invisible to every tool. (F13)
#
# USAGE:
#   scripts/lint-contracts.sh                        # the rules enabled by default
#   RULES=cli-no-render-funcs scripts/lint-contracts.sh
#   RULES=all scripts/lint-contracts.sh              # every rule, known debt included
#
# Reports every violating rule (file:line per hit) and exits 1 if any fired.

set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

# DEFAULT_RULES omits cli-no-embedded-interpreter: its one hit (internal/cli/kb_content.go, F13 — ~200
# lines of Python as a Go string) is known debt with no landed fix. It is implemented and selectable
# with RULES=cli-no-embedded-interpreter / RULES=all; move it here the day that command's programs move
# server-side or behind //go:embed.
DEFAULT_RULES="cli-no-raw-write,cli-no-render-funcs"
RULES="${RULES:-$DEFAULT_RULES}"

status=0

selected() {
	case ",$RULES," in
	*",all,"* | *",$1,"*) return 0 ;;
	*) return 1 ;;
	esac
}

# check <rule> <description> <extended-regex> — the file list arrives on stdin, one path per line.
check() {
	rule="$1"
	desc="$2"
	pattern="$3"
	files=$(cat)
	[ -n "$files" ] || return 0
	hits=$(printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 grep -nE -- "$pattern") || return 0
	printf '%s: %s\n' "$rule" "$desc" >&2
	printf '%s\n' "$hits" | sed 's/^/  /' >&2
	return 1
}

# go_sources <pathspec…> — tracked non-test Go files under the given roots.
go_sources() {
	# `|| true`: under pipefail an empty match set (a checkout with no Go files) must not read as a hit.
	git ls-files "$@" | { grep '\.go$' || true; } | { grep -v '_test\.go$' || true; }
}

if selected cli-no-raw-write; then
	go_sources 'internal/cli' |
		check cli-no-raw-write \
			"internal/cli must call a named client method returning (typed, raw, error), not Raw/RawScoped" \
			'\.(Raw|RawScoped)\(' || status=1
fi

if selected cli-no-render-funcs; then
	go_sources 'internal/cli' |
		check cli-no-render-funcs \
			"rendering belongs in internal/render as a pure function of server rows" \
			'^func render[A-Z][A-Za-z]*\(' || status=1
fi

if selected cli-no-embedded-interpreter; then
	go_sources 'internal' |
		check cli-no-embedded-interpreter \
			"no interpreter program embedded in a Go string literal" \
			'(python3|python|bash|sh) - ?<<' || status=1
fi

if [ "$status" -eq 0 ]; then
	printf 'lint-contracts: ok (rules: %s)\n' "$RULES"
fi
exit "$status"
