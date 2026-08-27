# rootcause-cli (`rc`)

A scriptable client that lets a project **consume its own rootcause data** and **change its own
config** — over rootcause's public JSON `/api/v1`, authed with an **OAuth access token** (you sign in
once with `rc auth login`; the CLI refreshes the token for you). **Fat client, thin server:** endpoints
return raw, token-scoped data; the CLI may digest/cluster/render it for you, and every such command also
emits the raw rows as **JSON when piped** so `| jq` always works and you can slice it your own way.

```console
$ rc status
Health: healthy

Sources:
  SOURCE      TOTAL  ERRORS
  Prompt API  12     0

$ rc ask "Do I still have open invoices?"   # simulate a support email, wait, print draft/note
$ rc ask --scenario raw "How many active subscriptions are past due?"
$ rc ask --effort pro "Retry this with a stronger model tier"
$ rc run list --kind prompt --limit 5 | jq '.runs[].run_id'
$ rc run list --outcome failed --learning             # learning candidates with failed verdicts
$ rc run events <id>        # full per-iteration trace (NDJSON when piped)
$ rc run trace <id>          # GET /runs/{id}/trace bundle (header + trace; JSONL when piped)
$ rc fleet actions --days 14 --action create_appointment --action update_appointment
$ rc dev learning evidence --plane triage --limit 50 -o json
$ rc dev learning evidence --plane deltas --include-bodies -o json
$ rc dev brain sync
$ rc dev brain promote --channel stable --sha "$(git rev-parse HEAD)"
$ rc dev brain status                 # verify stable/edge resolved SHAs before claiming success
$ rc project settings runtime set max_run_usd=5 'models.agent={"tier":"pro"}'
$ rc project settings behavior set persona.tone=warm channel.labeling_enabled=true
$ rc project triage policy get -o json
$ rc project triage rules ls -o json
$ rc project tenant settings get acme
$ rc project tenant profile get acme
$ rc project mailbox settings set <mailbox-id> persona.tone=direct
$ rc dev api routes | grep /trace
$ rc project env keys                  # key NAMES of the production grounding env (no values)
$ rc project env pull                  # sync that env to a local 0600 ./.env (for brain-dev --live)
```

## Install

You do **not** need Go installed — grab a prebuilt binary with the one-liner for your platform.

**macOS — Homebrew:**

```bash
brew install rootcause-org/tap/rc      # then: rc self update
```

> This is a **cask** (a prebuilt binary), not a source formula — it sidesteps the Homebrew sandbox/PTY
> install that fails on some macOS setups with `can't get Master/Slave device`. Quarantine is stripped
> automatically, so `rc` runs without a Gatekeeper prompt.
> Homebrew is the canonical macOS installation. Do not also install `rc` with Go or a standalone
> script; migrate an existing mixed setup with `rc self update --migrate`.

**Linux / WSL — install script** (no Homebrew or Go required):

```bash
curl -fsSL https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.sh | sh
```

Detects your arch, installs `rc` to `/usr/local/bin` (or `~/.local/bin`), and is idempotent — re-run to
upgrade. Knobs: `RC_VERSION=v0.5.1` to pin, `RC_ASSET_SHA256=…` to pin the reviewed archive digest,
and `RC_INSTALL_DIR=…` to choose where. On macOS the script
uses Homebrew unless `RC_INSTALL_DIR` is explicitly set.

**Windows (native PowerShell):**

```powershell
irm https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.ps1 | iex
```

Installs `rc.exe` under `%LOCALAPPDATA%\Programs\rc` and adds it to your user PATH. (On **WSL**, use the
Linux one-liner above — WSL is Linux.)

**From source (Go developers, development builds only):**

```bash
go install github.com/rootcause-org/rootcause-cli/cmd/rc@latest
```

Tagged Go installs report the embedded module version correctly, but Homebrew remains the one supported
end-user installation on macOS.

**Manual** — grab a tarball/zip from the [latest release](https://github.com/rootcause-org/rootcause-cli/releases/latest)
(`darwin`/`linux`/`windows` × `amd64`/`arm64`), extract `rc`, and put it on your PATH. On macOS, an
unsigned binary may be quarantined: `xattr -d com.apple.quarantine $(which rc)`.

### Upgrading

```bash
rc self update            # self-update to the latest release (Linux / WSL / Windows)
rc self update --check    # just say whether a newer version exists
rc self doctor            # show executing binary, PATH selection, install kind, duplicates
rc self update --migrate  # macOS: canonicalize on Homebrew; remove verified legacy Go copies
```

`rc self update` replaces its own binary with the latest release for your OS/arch (verifying the published
checksum first). On macOS it runs the Homebrew updater; a standalone or duplicate setup requires the
explicit, idempotent `--migrate` path. Migration installs/upgrades the cask, verifies its version, removes
only binaries whose Go build metadata proves they are legacy rootcause-cli installs, reshims mise, and
then verifies PATH selects the cask. Unknown binaries are reported for manual review, never deleted.

After installing or migrating, `which -a rc`, `rc --version`, `rc self update --check`, and
`rc self doctor` should all describe one PATH-visible binary at the latest published version.

## Sign in

`rc` authenticates with **OAuth**. Sign in once with `rc auth login`; it stores an access + refresh token in
`~/.config/rootcause/tokens.json` (0600) and refreshes the short-lived access token transparently on
every later command. The token's **scope is chosen on the browser consent screen** (a single project,
one tenant under a project, or — for a global admin — all projects); there is no key to paste.
Tenant-enabled project logins can be project-scoped, then `--tenant <slug>` chooses the run scope per
command.

```bash
rc auth login            # opens your browser (PKCE loopback), catches the redirect, stores the token
rc auth login --device   # headless/SSH: prints a short code you approve in a browser on any device
rc auth logout           # revoke the token server-side and clear it locally
```

`rc auth login` prints the full sign-in URL before trying to open a browser, so agents can surface that URL
to a human immediately. If the local browser opener fails, the command keeps waiting on the printed URL;
use `rc auth login --device` for true headless/SSH sessions where a browser cannot reach localhost.

**Let the brain checkout select the profile.** A brain repo (`rootcause-brain-<project>`) commits a
`.rootcause.toml` binding it to one project, so `rc` run anywhere inside it first looks for
a local token profile with the same name. If that profile exists, it uses it; otherwise it uses the
`default` profile and sends the brain project as the server-side `--project` scope where supported:

```bash
cd rootcause-brain-acme   # committed .rootcause.toml: project = "acme"
rc auth login                  # stores a token under the "acme" profile
rc auth status                 # profile: acme · project: acme · tenant if bound · auth: logged in
rc ask "…"                # just works
```

That gives two workflows: project developers can keep one token per project profile, while a global
admin can keep one all-projects token in `default` and still have each brain checkout auto-scope to its
own project. Main intent: the checkout chooses the project context; the profile only chooses which
local token to use.

### Headless cloud agents

For a trusted personal cloud-agent environment, keep machine credentials project-bound and let each
checkout select its own named variable:

```toml
# .rootcause.toml (committed; contains no secret)
project = "acme-staff"
machine_token_env = "RC_REFRESH_TOKEN_ACME_STAFF"
```

Store `RC_REFRESH_TOKEN_ACME_STAFF=<minted refresh token>` in the cloud environment. `rc` seeds the
`acme-staff` profile in its 0600 token store and refreshes the short-lived access token normally. A
single environment may hold several project-named variables; the current checkout's marker chooses the
profile. This reduces UI-selection mistakes, but it is not secret isolation: every session using that
environment can see every variable. Use separate environments for different users, trust boundaries, or
shared/public sessions.

Before any command endpoint runs, `rc` verifies that this credential is pinned to the marker's exact
project and rejects swapped or all-projects tokens. Removing the variable disables a cached machine
credential; a separately stored human OAuth login for the project still works locally. In a Claude
cloud session (`CLAUDE_CODE_REMOTE=true`), a missing named variable fails closed instead of using a
broader `default` token; local sessions retain their normal human OAuth fallback.

For safety, a marker-sourced machine token is accepted only against built-in production
(`https://app.replypen.com`); use a separate `rc auth login` profile for staging/custom
`ROOTCAUSE_BASE_URL` targets.

Mint one token per project and add only the scopes the workflow needs:

```bash
rc project token mint email=agent@example.com \
  scope='read config:read runs:read console:db' \
  --project acme-staff -o json
```

The refresh token is shown once. Never commit it, pass it on argv, or use an all-projects token merely
to simplify cloud setup. `console:db` also requires the bound user to have the project's developer/admin
capability; omit it when production database reads are not needed. Add `runs:trigger`, `actions:read`,
or other scopes only when the agent actually uses those planes. Verify a fresh session with
`rc auth status -o json` and one narrow read.

For an interactive install where no long-lived secret is present during setup, use the short canonical
installer when the environment can reach GitHub's release API:

```bash
curl -fsSL https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.sh | sh
```

Some repo-scoped proxies (including Claude Code web) allow Git/release downloads but deny release API
metadata for repositories outside the selected checkout. Resolve the tag over Git in that case:

```bash
set -euo pipefail
RC_VERSION="$(git ls-remote --refs --tags https://github.com/rootcause-org/rootcause-cli.git \
  | awk -F/ '{print $3}' \
  | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
  | sort -V \
  | tail -1)"
test -n "$RC_VERSION"
curl -fsSL https://raw.githubusercontent.com/rootcause-org/rootcause-cli/main/scripts/install.sh \
  | env RC_VERSION="$RC_VERSION" sh
```

Both paths install the latest release and verify its published checksum. The longer form is a proxy
compatibility workaround, not a generally superior installer. Prefer failing setup visibly; use
`|| true` only when a later startup hook explicitly verifies `command -v rc` and reports failure.

For a cached cloud image whose setup process can already read long-lived secrets, pin the reviewed
installer and CLI version. This avoids executing a mutable `main` branch with those secrets present:

```bash
set -euo pipefail
RC_VERSION=v1.16.0
RC_INSTALLER_SHA256=c5fcf883e78776382cb71b7cef199507c5ce03b57e006489fb0c15e97a62e340
# Copy the matching linux_amd64 or linux_arm64 digest from that release's checksums.txt.
RC_ASSET_SHA256='<reviewed 64-character archive digest>'
installer="$(mktemp)"
trap 'rm -f "$installer"' EXIT
curl -fsSL "https://raw.githubusercontent.com/rootcause-org/rootcause-cli/${RC_VERSION}/scripts/install.sh" \
  -o "$installer"
test "$(sha256sum "$installer" | awk '{print $1}')" = "$RC_INSTALLER_SHA256"
env RC_VERSION="$RC_VERSION" RC_ASSET_SHA256="$RC_ASSET_SHA256" sh "$installer"
```

The installer verifies the downloaded archive against the independently pinned digest instead of
trusting a mutable release checksum file. Upgrade a cloud image deliberately by reviewing the release,
then updating all three pinned values and rebuilding the snapshot. The example targets Linux cloud
agents; canonical macOS installs stay on Homebrew.

For Claude cloud network access, choose **Custom**, list only the RootCause API host
`app.replypen.com`, and keep **Also include default list of common package managers** checked. Claude's
default version-control list already includes the GitHub/raw/release-asset hosts used above; add them
explicitly only when deliberately disabling that default list.

Project-brain publishing is exact and OAuth-only: push the tested commit to GitHub, run `rc dev brain
sync`, then `rc dev brain promote --channel stable|edge --sha <full-40-character-SHA>`. On a templated
(multi-tenant) project you can see the promotion's verdict first with `rc dev brain preflight --sha
<SHA> [--channel stable|edge]`: the server compiles that commit for every tenant pinned to the channel and
lists any that would break or fall back to placeholder text, without moving anything (non-zero exit when
it would). Promote runs the same check itself and refuses a bad candidate, so preflight is optional. Promotion moves
only the named managed channel to that commit; it never promotes an ambient `main` tip. Finish with `rc
dev brain status` and confirm the channel's **resolved** SHA. `rc dev brain publish --channel … --sha …`
does the whole choreography in one gated command — sync, promote, then verify the channel resolved to
that exact SHA — and exits non-zero on any mismatch (`-o json` carries the receipt). Tenant overlays always run from their own
`main` and have no promotion route. A tenant-scoped login cannot move the shared project channel; sign
in with an authorized project-maintainer login instead. Explicitly narrowed tokens need the dedicated
`brain:promote` OAuth scope in addition to that project-level admin authority.

To eyeball what one tenant actually gets from a templated brain, run `rc dev brain render --tenant <slug>
[--path AGENTS.md] [--all] [--sha <SHA> | --channel stable|edge]`. The server compiles that tenant's
placeholders and `rc:branch` regions in memory (the deployed brain cache is untouched) and returns the
files exactly as `/brain` would mount them: a short header with the resolved commit, the fill/branch/
degradation counts, then each file under a `=== <path> ===` marker. Without `--sha`/`--channel` it uses
the tenant's currently resolved channel. Where preflight answers "would this commit break anyone",
render answers "what does this tenant read". Large output spills to
`.rootcause/output/brain-render-<tenant>/`, keyed by tenant only: rendering a second sha/channel
overwrites the first, so move it aside if you want to compare.

Tenant-brain repository access also stays server-side: from a tenant brain checkout, run
`rc dev brain developer invite <github-handle>`. The rootcause GitHub App creates or reuses the
repository invitation, and human output prints its acceptance URL; `-o json` returns the raw receipt.
Outside a checkout, select the exact repository with `--project <project> --tenant <slug>`.

**Base URL** is deliberately boring: production is hardcoded to `https://app.replypen.com`. The only
runtime override is `ROOTCAUSE_BASE_URL`, for deliberate staging/dev work. `rc auth login` uses the same
resolution, and `rc auth status` prints both the URL and its source (`built-in production` or
`ROOTCAUSE_BASE_URL`). Old `base_url` values in `~/.config/rootcause/config.toml`, `.rootcause.toml`,
or the token store do not steer normal commands. The legacy production host
`https://rootcause.probackup.io` is treated as an alias when an explicit env/token URL is canonicalized.

```bash
export ROOTCAUSE_BASE_URL=https://staging.your-rootcause-host
```

**Profiles** are the token-store keys. The profile is resolved as: explicit `--profile <name>` >
the brain marker's project if that token exists > `"default"`. `--profile` picks *which stored token*
a command uses.
`--project <id-or-name>` is **not** a token selector — it's a **server-side scope**: it keeps the active
token and names one project on supported endpoints (`?project=`), so an **all-projects admin token** can
review a single project (`rc fleet runs --project momentum-tools`) or trigger one (`rc ask --project
momentum-tools "…"`) without minting a per-project profile; a project-pinned token rejects any selector
that does not name its own project.
When a project scope is set, the CLI validates it against `rc project list` first and fails typos with a
hint to run `rc project list`.
On tenant-enabled projects, the active `rc auth login` may bind one tenant or the whole project. Plain
`rc ask "…"` works when the token is tenant-pinned; project-pinned logins pass `--tenant <slug>` per
workspace-producing command. Every executable command declares whether it applies project/tenant scope;
unsupported selectors fail before a request, and `--all` cannot be combined with either. Human
tenant-scoped output starts with `Scope: project / tenant`; JSON remains the raw API response. `rc auth
status` shows the login-bound project and tenant, when one is pinned.

For a whole-fleet review with an all-projects token, `rc fleet runs`, `rc fleet patterns`, and
`rc fleet health` take **`--all`**. The CLI lists the fleet (`rc project list`) and fans out per project —
`fleet runs --all` groups the digest by project with a fleet total, `fleet health --all` exits non-zero
if ANY project is unhealthy, and `fleet patterns --all`
clusters per project. `-o json` emits the merged `{projects:[…]}` shape. `--all` against a project-scoped
token is a friendly error (it needs an all-projects token), not a silent single-project run.

`rc fleet runs` also carries **`--timeline`** (per-day runs/errors/latency), off by default to keep the
digest scannable; the per-run rows always ride in `-o json` so the breakdown is re-derivable. Stuck runs
(`running` past a 30m clock with no finish) and a `FB` fallback flag are surfaced inline; every
worst-offender line carries the full triage tail (secs · turns · bash_err · FB). Heaviness is read off
**turns / bash / duration** — the CLI shows no spend or token counts on any surface, and no **serving
model identity** either: naming the rung that answered is as cost-reverse-engineerable as naming the
provider that served it, so the API projects none of it on any tier. `is_fallback` survives as the
content-free half — THAT the loop swapped rungs, never between which models. The model×cost×fallback
breakdown is a superadmin `db.py` query now, not a `rc` view.

`rc fleet actions` is the operator-only cross-run action index. It automatically pages a recent window
without downloading the much larger event feed or calling every run separately. Repeat `--action` and
`--status` to OR exact filters; statuses are `proposed`, `executing`, `succeeded`, `failed`, and
`canceled`. Human output includes each action-run/run id, timestamps and duration, exact structured
params, and the full freshly tokenized run URL by default. A settled failure renders as a `CLASS` column
plus an `Error:` detail line: the host-stamped infra classes (`executor_predispatch` — provably nothing
ran, retry-safe; `executor_error`, `no_executor`, `no_runner_url`, `attachment_fetch`) mean rootcause's
own machinery failed, anything else is the action's own domain refusal. The message is clamped to one
readable line in table/agent output. Piped/`-o json` output preserves the complete raw action rows —
including the untruncated `error_message`; explicit `--format agent` pins a one-line-per-action complete
view even through a pipe. `rc run actions` shows the same class column, narrowed server-side to the host
vocabulary (an action-authored class collapses to `action_error`) and without the message, which can echo
customer data.
The endpoint needs `console:action` plus operator/admin action-view authority because grounded params may
contain customer values.

Use bare **`--learning`** to keep runs with any dream-cycle signal, or select one with
`--learning=feedback|sent_delta|triage_skipped|triage_corrected`; the same filter works on
`rc run list`. Run-list tables show the matching safe boolean signals, and fleet tables/agent output
carry them as `LRN:` flags. JSON keeps the server rows unchanged.

Use **`--reviewed`** on `rc fleet runs` or `rc run list` for the human-audit corpus: every run with a
1–5 review score, including held-out eval runs. Operator JSON includes `review.score` and the optional
`review.comment`; this stays separate from `--learning=feedback`, which excludes held-outs.

`rc dev learning evidence` is intentionally JSON-only because feedback, deltas, and triage evidence
have different wire shapes. `--plane feedback|deltas|triage` narrows the corpus; delta bodies stay
omitted unless `--include-bodies` is explicit.

**`.rootcause.toml`** (committed, per brain) names the project + endpoint — no secret, safe to commit,
ships the binding with a clone. Optional gitignored **`.rootcause/local.toml`** can still set a local
tenant override for debugging. There is no longer any `.rootcause.secret.toml` — credentials live only
in the OAuth token store.

## Commands

The inventory below is generated from Cobra's offline command tree. Refresh it together with recursive
help using `go test ./internal/cli -update`.

<!-- BEGIN GENERATED COMMAND INVENTORY -->
| Command | Purpose |
|---|---|
| `rc admin catalog ls` | List catalog |
| `rc admin catalog upsert` | Create or update a catalog entry (keyed on key=) |
| `rc admin catalog` | Manage the integration catalog |
| `rc admin project add` | Create a project (name=… [default_tier=standard|pro|max] [egress_mode=wildcard|enforce]) |
| `rc admin project ls` | List projects |
| `rc admin project` | Manage box-level projects |
| `rc admin user add` | Create a user (email=… [admin=true] [password=…]) |
| `rc admin user ls` | List users |
| `rc admin user set` | Update a user ([admin=true|false] [password=…]) |
| `rc admin user` | Manage box-level users |
| `rc admin` | Box-level administration (users/projects/catalog; global-admin token) |
| `rc ask` | Trigger a run from a question and wait for the answer |
| `rc auth access` | Show what this token may do (scopes, writable keys, resources) |
| `rc auth login` | Sign in with OAuth (PKCE loopback by default, --device for headless) |
| `rc auth logout` | Revoke and clear this profile's stored tokens |
| `rc auth status` | Show the resolved profile/project/login tenant + sign-in status |
| `rc auth` | Manage local authentication and inspect access |
| `rc dev api openapi` | Dump the canonical OpenAPI document |
| `rc dev api routes` | Show the canonical API route manifest |
| `rc dev api` | Inspect the public API contract |
| `rc dev brain consolidate` | Queue the consolidation cron on demand |
| `rc dev brain developer invite` | Invite a GitHub user to this tenant brain repository |
| `rc dev brain developer` | Manage tenant brain developer access |
| `rc dev brain edit` | Queue a brain edit from a plain-language instruction (or STDIN) |
| `rc dev brain preflight` | Dry-run a channel promotion: which tenants would the candidate commit break? |
| `rc dev brain promote` | Promote an exact tested commit to a project brain channel |
| `rc dev brain publish` | Sync, promote an exact tested commit, and verify one project brain channel |
| `rc dev brain render` | Show a tenant's compiled brain projection |
| `rc dev brain status` | Show deployed brain cache status |
| `rc dev brain sync` | Fetch origin/main and refresh deployed brain cache |
| `rc dev brain` | Inspect, publish, and manage brain repositories |
| `rc dev console action list` | List available actions |
| `rc dev console action preflight` | Run action preflight/dry-run |
| `rc dev console action run` | Execute an action |
| `rc dev console action show` | Show one action manifest |
| `rc dev console action` | Inspect and execute guarded rootcause actions |
| `rc dev console bash list` | List cataloged brain scripts |
| `rc dev console bash run` | Run one command in the guarded workspace console |
| `rc dev console bash` | List or run workspace console commands |
| `rc dev console capabilities` | List direct production primitives available to this login |
| `rc dev console database list` | List available databases |
| `rc dev console database query` | Run a guarded production database query |
| `rc dev console database schema` | Fetch database schema, optionally one table |
| `rc dev console database` | Access guarded production databases |
| `rc dev console file get` | Fetch a workspace or /tmp file without the bash output cap |
| `rc dev console file` | Transfer files from a guarded workspace console |
| `rc dev console` | Use guarded production consoles |
| `rc dev context-export` | Render a project's full opening context offline (needs a rootcause host checkout) |
| `rc dev learning evidence` | List feedback, sent-edit, and triage evidence for consolidation |
| `rc dev learning` | Inspect learning and consolidation inputs |
| `rc dev mirror refresh` | Refresh mirrors and verify one repository at an exact commit |
| `rc dev mirror` | Refresh project source mirrors |
| `rc dev tools id gmail` | Translate Gmail hex/decimal/thread-f: ids + build a clickable URL |
| `rc dev tools id outlook` | Classify an Outlook/Graph id + tell you which DB column matches it |
| `rc dev tools id` | Translate provider message/thread ids |
| `rc dev tools provider detect` | Detect a domain's email backend (google/microsoft/other) from DNS |
| `rc dev tools provider` | Provider (channel) utilities |
| `rc dev tools` | Use local provider and identifier utilities |
| `rc dev` | Develop and inspect project behavior |
| `rc fleet actions` | Find actions across recent runs with exact params and run URLs |
| `rc fleet deploy-state` | Show the live host, brain, and mirror SHAs with their history |
| `rc fleet health` | Roll up project health (mirrors + dead-letters); exits non-zero when unhealthy |
| `rc fleet patterns` | Cluster recent failures and outbound endpoint patterns |
| `rc fleet runs` | Fleet digest of recent runs (flags, rates, worst offenders) |
| `rc fleet` | Operate and inspect project health |
| `rc project action-settings get` | Show current values (value / effective / default) |
| `rc project action-settings set` | Change values (sparse, validate-then-apply server-side) |
| `rc project action-settings` | Read or change action-plane config (operator-tier) |
| `rc project branding get` | Show current values (value / effective / default) |
| `rc project branding logo clear` | Remove the stored logo |
| `rc project branding logo set` | Upload a logo image (PNG/SVG/JPEG) |
| `rc project branding logo` | Set or clear the white-label logo image |
| `rc project branding set` | Change values (sparse, validate-then-apply server-side) |
| `rc project branding` | Read or change white-label branding (colours/name/public_base_url) |
| `rc project connection add` | Create a connection |
| `rc project connection ls` | List connections |
| `rc project connection probe` | Probe an integration capability grant |
| `rc project connection reveal` | Print a connection's secret (sensitive, shown once) |
| `rc project connection rm` | Revoke and delete a connection |
| `rc project connection rotate` | Rotate a connection's secret |
| `rc project connection` | Manage outbound integration connections |
| `rc project corpus download` | Download an export artifact (Markdown corpus or templates JSON) |
| `rc project corpus get` | Show one export |
| `rc project corpus ls` | List exports (id, kind, format, status, items, truncated, created/completed) |
| `rc project corpus mine-settings` | Mine a completed harvest for persona/triage setting proposals |
| `rc project corpus` | Read mailbox exports (harvest/survey/templates) |
| `rc project database controls get` | Show a database's controls |
| `rc project database controls set` | Change a database's controls (JSON object or k=v pairs; sparse) |
| `rc project database controls` | Read or change a database's access controls |
| `rc project database get` | Show one database |
| `rc project database ls` | List databases |
| `rc project database preview` | Preview the scoped rows a (tenant, principal) would see |
| `rc project database set` | Update a database |
| `rc project database` | Manage registered databases (list/read/update + access controls) |
| `rc project egress` | Inspect outbound endpoints, volume, and unattributed traffic |
| `rc project env diff` | Compare local ./.env to the server (NAMES-only drift); nonzero exit on drift |
| `rc project env keys` | List the key NAMES of the server's grounding env (log-safe, no values) |
| `rc project env pull` | Fetch the production grounding env and write ./.env (0600); prints NAMES + count, never values |
| `rc project env reveal` | Print one env var's value (sensitive, shown once) |
| `rc project env rm` | Delete one env var |
| `rc project env set` | Upsert one env var (value from STDIN by default; never echoed) |
| `rc project env` | Manage this project's sealed env secrets |
| `rc project github status` | Show the GitHub App install status (installed/account/install_url) |
| `rc project github` | Inspect the GitHub App install for this project |
| `rc project knowledge content export` | Export selected KB articles to a fresh local artifact directory |
| `rc project knowledge content list` | List KB collections without article bodies |
| `rc project knowledge content search` | Search KB articles and write matched articles to local artifacts |
| `rc project knowledge content` | List, search, and export knowledge articles |
| `rc project knowledge sync get` | Show current values (value / effective / default) |
| `rc project knowledge sync set` | Change values (sparse, validate-then-apply server-side) |
| `rc project knowledge sync` | Manage knowledge synchronization settings |
| `rc project knowledge` | Search knowledge content and configure synchronization |
| `rc project list` | List the projects this token can see (the fleet, for an all-projects token) |
| `rc project mailbox connect-imap` | Connect a generic IMAP/SMTP mailbox (live-probed before it's saved) |
| `rc project mailbox connect` | Print the dashboard Connections URL to start a provider's browser OAuth |
| `rc project mailbox harvest` | Start a local-synthesis harvest of a mailbox (optionally wait for the export) |
| `rc project mailbox imap-env` | Write an IMAP mailbox env file for local deep harvest (0600; values never printed) |
| `rc project mailbox ls` | List watched mailboxes (id, provider, email, status, tenant, expiry, error) |
| `rc project mailbox mode` | Set the mailbox watch, processing, and delivery mode |
| `rc project mailbox password-link` | Print the no-login password link for an IMAP mailbox (also used for rotation) |
| `rc project mailbox seed-imap` | Store an IMAP mailbox's server settings without a password and print its no-login password link |
| `rc project mailbox settings get` | Show settings with resolved provenance |
| `rc project mailbox settings set` | Patch settings (nested; key= or --unset clears local override) |
| `rc project mailbox settings` | Read or edit nested mailbox settings (persona/channel) |
| `rc project mailbox templates` | Export Gmail canned responses as a separate JSON artifact |
| `rc project mailbox test` | Re-run the live IMAP/SMTP check on a connected mailbox and print the checklist |
| `rc project mailbox` | Manage watched mailboxes (the channel plane's inbox watch) |
| `rc project member add` | Create a member |
| `rc project member ls` | List members |
| `rc project member rm` | Delete a member |
| `rc project member` | Manage project members |
| `rc project model-key openrouter clear` | Remove the stored OpenRouter key |
| `rc project model-key openrouter reveal` | Print the stored OpenRouter key (sensitive, shown once) |
| `rc project model-key openrouter set` | Store the OpenRouter key (from STDIN by default; never echoed) |
| `rc project model-key openrouter` | Manage the OpenRouter API key (set/clear/reveal) |
| `rc project model-key` | Manage model-provider credentials |
| `rc project rename` | Rename the active project slug and brain repo |
| `rc project repo add` | Create a repo |
| `rc project repo ls` | List repos |
| `rc project repo rm` | Delete a repo |
| `rc project repo set` | Update a repo |
| `rc project repo` | Manage source repos (mirrors + per-repo PR config) |
| `rc project senders allow` | Never treat mail from <pattern> as spam |
| `rc project senders block` | Always treat mail from <pattern> as spam |
| `rc project senders ls` | List the spam allow and block rules |
| `rc project senders rm` | Delete a spam rule by id |
| `rc project senders` | Manage the spam allow/block lists (never-spam / always-spam) |
| `rc project settings behavior get` | Show settings with resolved provenance |
| `rc project settings behavior set` | Patch settings (nested; key= or --unset clears local override) |
| `rc project settings behavior` | Read or edit nested project settings (persona/channel) |
| `rc project settings describe` | Explain one config key (type, enum, scopes, default, help) |
| `rc project settings runtime get` | Show current values (value / effective / default) |
| `rc project settings runtime set` | Change values (sparse, validate-then-apply server-side) |
| `rc project settings runtime` | Manage flat runtime settings |
| `rc project settings schema` | Show the config registry (fields, types, scopes, defaults) |
| `rc project settings` | Read, change, and describe project settings |
| `rc project tenant add` | Create a tenant |
| `rc project tenant get` | Show one tenant |
| `rc project tenant ls` | List tenants |
| `rc project tenant profile get` | Show a tenant's projection/profile values |
| `rc project tenant profile schema` | Dump the tenant profile JSON schema |
| `rc project tenant profile set` | Edit tenant projection/profile values |
| `rc project tenant profile` | Read or edit tenant projection/profile values |
| `rc project tenant set` | Update a tenant |
| `rc project tenant settings get` | Show settings with resolved provenance |
| `rc project tenant settings schema` | Dump the hierarchy settings schema (debug/reference) |
| `rc project tenant settings set` | Patch settings (nested; key= or --unset clears local override) |
| `rc project tenant settings` | Read or edit nested tenant settings (persona/channel) |
| `rc project tenant` | Manage tenants (sub-scopes below a project) and their settings |
| `rc project token ls` | List tokens |
| `rc project token mint` | Mint a new token (refresh token shown once) |
| `rc project token revoke` | Revoke a token |
| `rc project token` | Manage API tokens |
| `rc project triage policy get` | Show triage policy guidance |
| `rc project triage policy set` | Replace triage policy guidance |
| `rc project triage policy` | Read or edit free-form triage guidance |
| `rc project triage rules add` | Create a triage hard rule |
| `rc project triage rules ls` | List triage hard rules |
| `rc project triage rules rm` | Delete a triage hard rule |
| `rc project triage rules set` | Patch a triage hard rule |
| `rc project triage rules` | Read or edit deterministic triage rules |
| `rc project triage` | Read or change mail triage policy and hard rules |
| `rc project` | Manage project configuration and resources |
| `rc run actions` | Show the safe action lifecycle history |
| `rc run brain-diff` | Show the brain commit written by a run |
| `rc run debug` | Decompose a run into local debug artifacts |
| `rc run egress` | Show outbound gateway connections and HTTP attempts |
| `rc run events` | Show the full per-event trace |
| `rc run feedback` | Record score/comment feedback on a run's trace, or mark it processed |
| `rc run list` | List recent runs (filterable) |
| `rc run process-thread` | Process a triage-skipped or security-blocked inbox thread |
| `rc run retry` | Re-run a run (optionally at a different tier); prints the new run id |
| `rc run show` | Show one run |
| `rc run thread` | Trace one provider/local thread or session through pipeline, run, and placement |
| `rc run trace` | Show the whole run bundle |
| `rc run` | Inspect and manage the run lifecycle |
| `rc self completion` | Generate a shell completion script |
| `rc self doctor` | Diagnose the active rc install, PATH copies, scope, and updates |
| `rc self update` | Update rc to the latest release (self-update) |
| `rc self` | Manage the rc installation and shell integration |
| `rc status` | Health summary + recent runs |
<!-- END GENERATED COMMAND INVENTORY -->

Global flags: `--profile <name>` picks the stored token; `--project <id-or-name>` scopes supported
requests to one project server-side and is validated against `rc project list` before use (useful for
all-projects tokens outside a brain checkout or as an override; inside a brain checkout the
`.rootcause.toml` project is used automatically when falling back to `default`);
`--tenant <slug>` explicitly selects a tenant where supported; it is required for workspace-producing
commands when a tenant-enabled project login is project-pinned. `--scope project|tenant` forces request
routing: `project` clears any resolved tenant (a `--tenant`, a brain checkout, or a tenant-bound login)
so a tenant-capable command hits the project route; `tenant` requires a resolvable tenant. `-o json|table`
forces output. Large output spills to `.rootcause/output/` by default; use `--out-dir <dir>` or
`RC_OUTPUT_DIR` to change that, `--no-preview` to print paths/metadata only, and `--raw-output` to
preserve exact full stdout.

`rc ask --brain-ref dev/<branch>` runs the question against a **non-main brain ref** — the project
dev's "test without pushing main" loop. Push a `dev/*` branch to your brain first (`git push origin
dev/<branch>`); the server runs the real loop against it and flags any actions/PRs as test.

`rc ask` defaults to `--scenario email`: it sends an explicit `scenario=email` to the Prompt API and
wraps the prompt as one synthetic inbound message from `--from` with `--subject` (or a compact prompt
first line). Use this for high-fidelity brain-dev checks: tone, notes, actions, PR proposals, and
declines are rendered like a reviewable support result. Use `--scenario raw` for direct investigations;
the CLI sends `scenario=raw` and prints one Markdown answer.

`rc ask --file path/to/file.pdf` uploads a local file as an inbound attachment on the synthetic
message. Repeat it for multiple files; relative paths are resolved from the current working directory.
Caps are checked locally before upload and mirror the server's: **max 4 files, 5 MiB each, 15 MiB
total**. `--attach` is a deprecated alias for `--file` (still merged into the same list).
The backend gives each file a real `attachment_id`, so hosted actions with `type: attachment` params can
be proposed against the same ID shape as production email. Action proposal/execution still depends on
the project's action plane and catalog being enabled.

`rc ask --effort pro|max` is a per-run escalation knob. It maps to rootcause's model tiers, not raw
provider effort values; use it when you explicitly want a stronger retry. Omit it, or pass
`--effort default`, for normal behavior.

`rc ask --principal-kind <K> --principal-id <ID>` asserts a **principal** on the run — a structured
identity that scopes the run's read-only data access to that entity's rows (e.g.
`--principal-kind kampadmin_person --principal-id <person-uuid>`). Both flags are a pair (supply both
or neither); `--asserted-by`/`--assurance` optionally refine the assertion and require the pair. The
principal is dormant unless the project declares `scope_claims`; without that config the server discards
it. Tenant binding stays the explicit `--tenant` slug — it is not part of the principal. A
principal-bearing submit never falls back to the legacy body, so a stale server rejects it rather than
silently dropping the scope.

#### `--session` — multi-turn threading

`--session <id>` threads a run onto a session so follow-up turns build on the earlier ones. The id is
**client-chosen** (any stable string — `session_id` is the join key, not `run_id`); reuse the same id
across turns and the server warm-starts each follow-up off the prior turns on that session:

```bash
rc ask --session homer-42 "Hello, my name is Homer. When can I have a dental appointment?"
rc ask --session homer-42 "Okay, let's go for Thursday."   # same session → warm-started
```

The server keys continuity on `(project, session_id, kind=prompt)` and injects a digest of **what the
earlier turns ran and how each ended** (the command trail) — *not* the prior conversation or answer
text. So a follow-up knows which queries already dead-ended, but does **not** yet replay the prior
turn's answer; phrase follow-ups to stand on their own rather than referring back anaphorically ("the
second option"). `--no-wait` prints just the `run_id`; the `session_id` is the one you passed in.

Output auto-detects: **TTY → table, piped → JSON**. Force with `-o json` / `-o table`. API errors are
surfaced verbatim (`CODE: message`) with a non-zero exit. `RC_PROJECT` and `RC_TENANT` provide the
defaults for their matching global flags. HTTP requests time out after 10 minutes by default
(`RC_HTTP_TIMEOUT=2m` overrides it); safe reads retry 429/5xx responses three times.

### Composable console primitives

Inline database reads stay capped at 500 rows. A truncated inline result fails closed with exit 3;
use `--allow-truncated` only when a partial answer is intentional, or `--all` to stream every page.
An `--all` query must end in an `ORDER BY` that totally orders the rows (include a unique tiebreaker);
its page size defaults to 5000 and `--limit` cannot exceed 5000. Paging is offset-based, so it is exact
for a stable dataset; concurrent inserts/deletes can shift later pages even with a total order. Ordered columns plus array rows preserve
duplicate column names, UUIDs, numerics, timestamps, and binary values losslessly. Dates render as
`YYYY-MM-DD`, intervals as PostgreSQL text, and `bytea` as base64. Explicit formats are `json`, `ndjson`,
`csv`, and `tsv`:

```bash
rc dev console database query prod @report.sql --all --format csv --out ./report.csv
printf 'SELECT id, amount FROM invoices WHERE state = @state' |
  rc dev console database query prod - --param state=open --all --format json --out - |
  jq '.rows[]'
```

SQL and bash commands accept a literal argument, `-` for stdin, or `@file`. Console SQL uses `@key`
placeholders; repeated `--param k=v` values are bound server-side as text and never interpolated. The
brain runtime's in-process `lib.db.query` API instead uses its driver's `%s` placeholders. `--limit >500`
requires `--all`. Multi-statement SQL and cross-database queries remain unsupported.

`--out <path>` atomically writes data there and prints a JSON manifest. `--out -` writes data directly
to stdout. `--out auto` writes a unique `.rootcause/output/<command>-<runid8>.<ext>` artifact, so
concurrent calls do not clobber each other. Fetch a brain-side spill without the bash 64 KiB cap with:

```bash
rc dev console file get /tmp/rootcause-out/report-abc123.csv --out ./report.csv
```

Stable process exits: `0` success, `1` usage, `2` auth/scope, `3` truncated, `4` remote bash
non-zero/timeout, `5` server/network. In JSON/auto-piped mode, command errors use
`{"error":{"code","message","status","fields"}}` on stdout. A bash result is emitted before exit 4
so its `exit_code`, `timed_out`, stdout, and stderr remain inspectable.

Compatibility note for scripts upgrading from rc 1.18: query spills now use unique
`.rootcause/output/console-db-query-<run8>.*` paths rather than the old fixed
`.rootcause/output/console-db-query/response.json`, and a result above the inline 500-row cap exits 3
instead of succeeding with partial data.

### `rc project env` — self-serve grounding-env sync

A project's grounding scripts read their credentials (PG DSN, Stripe key, …) from a gitignored `.env`
in the brain clone. `rc project env` lets a project admin or developer with secrets access sync that
**production** env to their laptop over OAuth — the self-serve equivalent of the operator-only
`scripts/rc_env.py --pull` (which needs AWS/SSM access). Run it **from inside the brain clone** (it
reads/writes `./.env`):

```bash
rc project env keys                 # what keys exist (names only — safe to paste/log)
rc project env pull                 # write ./.env at 0600 — then `brain-dev --live` can run grounding locally
rc project env diff                 # has my local ./.env drifted from production? (names-only; exit≠0 on drift)
printf %s "$SECRET_VALUE" | rc project env set key=FOO_API_TOKEN
rc project env rm FOO_API_TOKEN
rc project env reveal FOO_API_TOKEN # prints the value once; sensitive
```

> **Secret hygiene:** `keys`/`diff` are names-only; `pull` writes values only to the 0600 file; `set`
> reads from STDIN by default and never echoes. `reveal` is the deliberate exception: it prints one live
> secret value for copy/pipe use and audits the key name. The pulled `.env` holds **real production
> secrets** on your laptop — treat it like a password file (it's gitignored in every brain repo). A
> tenant-enabled bulk pull uses the tenant bound to your `rc auth login`, or requires `--tenant` when the
> login is project-pinned.

**Adding a database:** there is no `rc project database add` — a database exists because a sealed
grounding key named `<PROJECT>_<DBKEY>_DSN` exists. `printf %s "$DSN" | rc project env set
key=PROBACKUP_BACKUPS_DSN` registers the DB the brain reaches as `lib.db(db="backups")` (use a read-only
role; the rootcause box must be network-allowlisted). Confirm it resolves with `rc dev console database
list`, then annotate it (`rc project database set PROBACKUP_BACKUPS_DSN description=…`) so it appears in
`rc project database ls`. `*_WRITE_DSN` keys belong on `--plane action`, not the grounding plane.

`rc project env set/rm/reveal` targets the grounding plane by default (`/api/v1/env_grounding`), which is
injected into normal read-only runs. Per-key commands target a tenant env only when the OAuth token
itself is tenant-bound; `--tenant` does not retarget `set/rm/reveal`. `--plane action` targets
`.env.action` (`/api/v1/env_action`), the operator-only write-plane for hosted actions; it is
project-level and never enters normal runs.

## Releasing

Use the script — it does the whole cycle reliably and verifies each part:

```bash
scripts/release.sh patch     # 0.1.0 -> 0.1.1  (also: minor | major | vX.Y.Z | --dry-run)
```

A release is **four things that must land together**, which is why a bare `git tag` isn't enough:

1. the exact tested commit pushed and verified on **`origin/main`**;
2. the **git tag** `vX.Y.Z` at that exact commit;
3. the **GitHub Release** + prebuilt binaries — the [release workflow](.github/workflows/release.yml)
   builds every OS/arch via [GoReleaser](https://goreleaser.com) and attaches archives + checksums;
4. the **Go module proxy** ingesting the tag, so consumers' `go get …@latest` resolves the new version
   instead of a stale pseudo-version (the step that's easy to forget by hand).

The script gates on `go build/vet/test`, refuses a dirty/behind/diverged checkout, then explicitly
pushes the tested SHA to `origin/main` and verifies the remote ref before it creates the tag. It waits
for binaries and warms the proxy afterward. Local `main` may be ahead of origin; publishing it is part
of the release. See [`.claude/skills/release/SKILL.md`](.claude/skills/release/SKILL.md) for the full
runbook and manual fallback.

**Homebrew** is wired up: each release, GoReleaser commits an updated `Casks/rc.rb` **cask** to the
public [`rootcause-org/homebrew-tap`](https://github.com/rootcause-org/homebrew-tap) repo (the
`homebrew_casks:` block in [`.goreleaser.yaml`](.goreleaser.yaml)). It authenticates with the
`HOMEBREW_TAP_GITHUB_TOKEN` repo secret — a token with `contents:write` on the tap repo, since the
default `GITHUB_TOKEN` is scoped to this repo only and can't push to a second one.

It's a **cask** (prebuilt binary), not a source **formula**, on purpose: a non-bottled formula installs
through a Homebrew sandbox + Ruby PTY that fails on some macOS setups (`can't get Master/Slave device`);
a cask just links the binary. A cask is macOS-only and can't share the name `rc` with a formula (bare
`brew install …/rc` would pick the formula — the broken path), so the old `Formula/rc.rb` was deleted
from the tap and a `tap_migrations.json` (`{"rc":"rc"}`) added there so existing installs migrate
formula→cask on `brew upgrade`. Linux/WSL/Windows install via [`scripts/install.sh`](scripts/install.sh)
and [`scripts/install.ps1`](scripts/install.ps1), which need no Homebrew.

See [`SKILL.md`](SKILL.md) for the architecture and how to add a command.
