# `rc` command-surface review

Audit: CLI HEAD `3cbfd56`, production discovery + read-only checks on `pj-mailbox` and DentAI / `de-kies`, 2026-07-10.

## Decision

The CLI is functionally rich but has no information architecture: 43 application commands share one alphabetical root, mixing daily work, project setup, tenant operations, production consoles, secrets, API discovery, and box administration.

Move to **9 visible roots**, keeping every old path as a hidden compatibility shim:

```text
rc
├─ status
├─ ask
├─ run       list | show | events | trace | debug | brain-diff | thread | retry | feedback
├─ project   list | rename | settings | tenant | mailbox | triage | senders | model-key
│            connection | knowledge | corpus | database | repo | member | token
│            branding | env | github | action-settings
├─ dev       brain | console | learning | api | tools
├─ fleet     runs | health | patterns
├─ admin     user | project | catalog
├─ auth      login | logout | status | access
└─ self      update | completion
```

Root help headings: **Start**, **Manage**, **Develop**, **Operate**, **Local**. Help remains offline and stable; permissions do not dynamically change the command tree.

## Evidence

| Signal | Finding |
|---|---|
| Root exposure | 43 application roots (excluding Cobra `help` / `completion`) |
| Full exposure | 169 command nodes; 129 leaf paths, plus executable `run <id>` |
| Root help | One unclassified alphabetical list for at least four personas |
| API churn | Live manifest: 287 `/api/v1` routes, 83 deprecated aliases |
| Status UX | `rc status` printed the default 50-run page after its summary—not an “at a glance” landing |
| Docs drift | README omits `spam`, `access`, `explain`, `export mine-settings`, `mailbox imap-env`, and `action config` |
| Help defects | Generated CRUD copy includes “List tenantss/databasess” and “Create a connections” |
| Flag overload | `ask` has 14 non-help flags; IMAP connect has 9 |

The collisions are conceptual, not merely numeric:

- `status`, `runs`, `fleet`, `health`, and `patterns` look interchangeable. `status` / `runs` share `/runs`; `health` is a different infrastructure endpoint with a monitoring exit-code contract.
- `run`, `runs`, `thread`, and `dream evidence` split one run/learning lifecycle.
- `project` / `projects`, `database` / `db`, and `access` / `capabilities` differ in important but undiscoverable ways.
- “Settings” are fragmented across `config`, `config hierarchy`, tenant settings/profile, mailbox settings, action config, KB config, branding, database controls, env, and OpenRouter secrets.
- `kb` mixes article work with sync configuration. `export` means mailbox corpus, while `kb export` means KB artifacts.
- CRUD verbs vary (`ls/list`, `get/show/status`, `add/mint/upsert`, `rm/revoke/clear`).
- Persistent project/tenant/output flags appear even where they have no meaning.

### Tenant scope is priority zero

The real DentAI workflow proves the hierarchy is normal, not an edge case: the admin token sees 14 projects; DentAI has 11 tenants and 5 watched mailboxes across tenants.

From DentAI / `de-kies`:

- `status` and `health` were identical with and without a supplied `--tenant de-kies`.
- `mailbox ls --tenant de-kies` returned mailboxes for five tenants.
- Tenant settings/profile require an explicit target even in a tenant brain. That is a safe invariant for tenant-record access, but the grammar/context does not explain the distinction.

Today a supplied tenant selector can be silently ignored, honored, or rejected depending on the command. Ignoring it can broaden reads without warning. Before regrouping, every command must declare `project`, `tenant`, and `all-projects` support and either apply a selector or reject it. Tenant-record commands should take an explicit positional tenant slug rather than infer one from brain context.

Human output should start with `Scope: dentai / de-kies` when tenant-scoped. JSON remains raw. Do not add mutable `rc use` state; explicit flags, login scope, and brain binding are safer.

## Canonical moves

| Current | Canonical | Reason |
|---|---|---|
| `runs` | `run list` | One run lifecycle noun |
| `run <id> --events/--full/--debug/--brain-diff` | `run events/trace/debug/brain-diff <id>` | Remove mutually exclusive view flags; keep `run <id>` as `show` shorthand |
| `thread <id>` | `run thread <id>` | Run navigation |
| `projects`; `project rename` | `project list`; `project rename` | Singular resource namespace |
| `health`; `fleet`; `patterns` | `fleet health/runs/patterns [--all]` | One operations family while preserving selected-project and optional all-project fan-out |
| `config*`, `schema`, `explain` | `project settings get/set/describe/schema` | One settings entry point; remove “hierarchy” jargon |
| `triage`; `spam` | `project triage`; `project senders` | One mail-decision area; `senders` matches the canonical API noun |
| `mailbox pause/resume/process on/off` | `project mailbox mode <id> off\|watch\|shadow\|live` | One user-facing state; all four old API routes are deprecated |
| `db`; `database` | `dev console database`; `project database` | Clearly separate querying data from registering data sources |
| `bash`, `action`, `capabilities` | `dev console bash/action/capabilities` | They already share the console API and audience |
| `brain`; `dream evidence` | `dev brain`; `dev learning evidence` | Brain development and learning workflow |
| `routes`, `openapi`; `access` | `dev api routes/openapi`; `auth access` | API plumbing vs token permissions |
| `id`, `provider` | `dev tools id/provider` | Useful but specialist/local utilities |
| `login/logout/whoami`; `upgrade` | `auth login/logout/status`; `self update` | Familiar lifecycle groups |
| `config openrouter-key` | `project model-key openrouter set/clear/reveal` | Model credentials stay separate from ordinary settings and env |
| KB `list/search/export`; KB `get/set` | `project knowledge content`; `project knowledge sync` | Separate articles from connector configuration |
| `mailbox harvest`; `export *` | `project mailbox harvest`; `project corpus *` | Start and consume one corpus workflow in one area |
| `action config`; action execution | `project action-settings`; `dev console action` | Configuration and production execution are different risk planes |

Tenant profile remains separate from tenant behavior settings: the API contract explicitly defines profile as onboarding/projection data, not hierarchy settings.

## Settings end state

Present one human settings model, backed by `/meta/schema`:

- `get`: one grouped table with value, effective value, source, scope, and allowed levels.
- `set`: resolve each key to its owning endpoint from schema metadata.
- Reject a write containing keys from multiple endpoint families until the server can apply it atomically.
- Keep secrets (`env`, provider credentials, OpenRouter key) visibly separate from ordinary settings.
- Preserve legacy commands’ exact raw JSON. A canonical wrapper may be cleaner only if `-o json` includes the complete raw underlying endpoint responses without normalizing or dropping fields.

An interim naming step can expose `settings runtime` (current flat bag) and `settings behavior` (current hierarchy) before the server surfaces one atomic model.

## Migration

1. **Correct scope first.** Add per-command scope metadata/tests; tenant selectors cannot be silently ignored. Add the TTY scope header. Treat scope-broadening fixes as explicit correctness/security exceptions to legacy output compatibility.
2. **Organize without breaking.** Add Cobra command groups, fix generated grammar, generate/test README command inventory, and make `status` compact.
3. **Add canonical paths.** Reuse the same runner functions/client methods. Keep old exact paths registered as `Hidden: true`, absent from help and completion.
4. **Use canonical APIs.** New paths call the non-deprecated route advertised by discovery, using the project tree where one exists. Keep authoritative exceptions such as the flat scalar settings bag, `/meta/*`, and OAuth. Replace mailbox granular state with `mode`.
5. **Unify settings.** Schema-driven presentation first; cross-family writes only after atomic server support.
6. **Retire cautiously.** Preserve old stdout/stderr, JSON, secret handling, and exit codes except for declared scope-safety fixes. Remove shims only with command-path telemetry and a major release; cheap aliases may remain indefinitely.

Do not emit deprecation noise into scripts. If warnings are added later, emit only on a TTY. Never collect arguments in telemetry; they can contain prompts, SQL, IDs, and secrets.

## Guardrails

- Default root help: at most the 9 roots above.
- Every root command has an audience/help group; fail tests on an ungrouped addition.
- Every command declares accepted scopes; fail when a supplied selector would be ignored.
- Canonical commands may not call a discovery route marked deprecated.
- Canonical `-o json` includes complete raw endpoint inputs, even when wrapped for multi-endpoint views.
- Golden equivalence tests cover hidden legacy vs canonical JSON, stdout/stderr, and exit codes, with named exceptions for scope-safety fixes.
- Recursive help and generated README inventory must stay fresh in CI.
- Keep `status` and infrastructure `health` distinct; keep `database` registration and console queries distinct.
- Move or hide Cobra's generated `completion` command so it does not become a tenth visible root.

## Recommended first implementation slice

One low-risk release:

1. Fix tenant-scope behavior and add scope-contract tests.
2. Add grouped root help and a recursive help freshness test.
3. Introduce `run list/thread`, `fleet runs/health/patterns`, `dev console`, and `dev api`; hide old aliases.
4. Ship `mailbox mode` on the canonical endpoint; retain old lifecycle shims.
5. Regenerate README/help and fix CRUD plurals.

This yields the largest comprehension gain without changing automation contracts. Settings consolidation can follow as a separately tested API/CLI change.
