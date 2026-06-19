# rootcause-cli (`rc`)

A thin, scriptable client that lets a project **consume its own rootcause data** and **change its own
config** — over rootcause's public JSON `/api/v1`, authed with the project's existing **Prompt API
bearer key**. No business logic of its own: every command is one API call, rendered as a table on a
terminal or **JSON when piped** so `| jq` always works.

```console
$ rc status
Health: healthy

Sources:
  SOURCE      TOTAL  ERRORS
  Prompt API  12     0

$ rc runs --kind prompt --limit 5 | jq '.runs[].run_id'
$ rc run <id> --events        # full per-iteration trace (NDJSON when piped)
$ rc config set max_run_usd=5 default_tier=pro
```

## Install

You do **not** need Go installed — grab a prebuilt binary.

**Homebrew (macOS / Linux)** — once the tap is published:

```bash
brew install rootcause-org/tap/rc
```

**Prebuilt binary** — from the [latest release](https://github.com/rootcause-org/rootcause-cli/releases/latest)
pick your OS/arch (`darwin`/`linux`/`windows` × `amd64`/`arm64`), then:

```bash
# macOS example (arm64). Adjust the version/arch to the asset you downloaded.
curl -sSL https://github.com/rootcause-org/rootcause-cli/releases/latest/download/rc_<ver>_darwin_arm64.tar.gz \
  | tar -xz && sudo mv rc /usr/local/bin/
rc --version
```

> macOS Gatekeeper may quarantine an unsigned binary — `xattr -d com.apple.quarantine $(which rc)` if it
> refuses to run.

**From source (Go devs)**:

```bash
go install github.com/rootcause-org/rootcause-cli/cmd/rc@latest
```

## Configure

`rc` needs the project's **Prompt API bearer key** (the key resolves the project server-side — there's
no `--project` flag) and the API base URL.

```bash
export ROOTCAUSE_API_KEY=rcl_…
export ROOTCAUSE_BASE_URL=https://your-rootcause-host   # default: http://localhost:8080
```

Or use `~/.config/rootcause/config.toml` with named profiles (`--profile <name>`):

```toml
[default]
api_key  = "rcl_…"
base_url = "https://your-rootcause-host"

[profiles.staging]
api_key  = "rcl_…"
base_url = "https://staging.your-rootcause-host"
```

Precedence: a value in the selected profile **overrides** the matching env var, which overrides the
built-in default. Keys live in env/config **by name** — never commit them.

## Commands

| Command | Does |
|---|---|
| `rc status` | recent runs + health summary (the no-filter index view) |
| `rc runs [--limit N] [--kind email\|prompt\|mcp\|analysis] [--category …] [--before <id>]` | filterable run list, keyset-paged |
| `rc run <id>` | one run, high level |
| `rc run <id> --events` | full per-event trace (NDJSON in JSON mode) |
| `rc config get` | effective settings + box defaults |
| `rc config set k=v [k=v…]` | change settings (validated server-side) |
| `rc --version` · `rc help` | |

Output auto-detects: **TTY → table, piped → JSON**. Force with `-o json` / `-o table`. API errors are
surfaced verbatim (`CODE: message`) with a non-zero exit.

## Releasing

Tag and push — the [release workflow](.github/workflows/release.yml) builds every OS/arch via
[GoReleaser](https://goreleaser.com) and attaches the archives + checksums to the GitHub Release:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

**To enable Homebrew** (`brew install rootcause-org/tap/rc`), one-time: create a public
`rootcause-org/homebrew-tap` repo, add a `HOMEBREW_TAP_GITHUB_TOKEN` secret (a token with
`contents:write` on that tap repo — the default `GITHUB_TOKEN` can't push to a second repo), then
uncomment the `brews:` block in [`.goreleaser.yaml`](.goreleaser.yaml) and the env line in the release
workflow.

See [`SKILL.md`](SKILL.md) for the architecture and how to add a command.
