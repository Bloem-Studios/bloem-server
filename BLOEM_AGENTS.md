# Bloem Server — agent instructions (fork overrides)

`AGENTS.md` (and `CLAUDE.md`, which links to it) is Silo's upstream file, kept verbatim so
upstream merges stay clean. This file carries Bloem's additions and overrides. **Where the
two disagree, this file wins.** Fork provenance and merge policy: [FORK.md](FORK.md). Bloem's
documentation index: [docs/bloem/README.md](docs/bloem/README.md).

## Naming

Read "Silo" in `AGENTS.md` as **Bloem** wherever it names this product: this repository is
Bloem Server, the Go backend for Bloem. Compatibility identifiers (module path, `cmd/silo`,
`SILO_*` environment variables, `X-Silo-*` headers, protocol fields) keep their Silo names;
see [FORK.md](FORK.md).

## API posture

Other people's clients use the Silo-compatible v1 projection (`/api/v1`); Bloem's own native
extensions live under `/api/bloem/v1` ([docs/bloem-api-reference.md](docs/bloem-api-reference.md)).
Read the upstream "API contract rules" section with that in mind: a client-visible change must
fit the current v1 posture, and new features still expose a capability endpoint.

## Live TV is in scope (overrides the Non-goals section)

**Live TV is a maintained Bloem feature**: HDHomeRun-compatible tuners, authenticated
Xtream live providers, guides, channel playback, DVR rules and recordings are in scope.
Xtream is live-only and uses fixed reviewed endpoints with encrypted credentials; it is
not VOD/series ingestion or an Xtream server API. Arbitrary remote-URL shortcuts (including
`.strm`) and generic remote-stream ingestion remain out of scope; see
[Bloem's edition of docs/non-goals.md](docs/bloem/overlays/non-goals.md)
and [docs/architecture/xtream-live-tv.md](docs/architecture/xtream-live-tv.md) for the boundary.

## Docs hygiene (replaces the upstream paragraph)

New implementation plans and specs are ephemeral working artifacts, not
maintained documentation. `docs/superpowers/` is gitignored for new files: write new plans there
(or in scratch space), but do not add them to a change. The already tracked files in that
directory are historical records; preserve their historical facts and add a frozen-snapshot banner
when one reads as current operating guidance. Before a branch merges, distill durable invariants,
protocols, and security rules into `docs/architecture/`. The code is the source of truth; a doc
that disagrees with the code is wrong. Any committed doc must not contain local absolute filesystem
paths or transient worktree IDs — use repository-relative paths and wording like "Commands assume
the repository root is the cwd." `make verify-local-paths` enforces this.

Keep Bloem-specific documentation out of Silo-owned files: add it to a Bloem-owned doc (for a
change to an upstream doc, its overlay under `docs/bloem/overlays/`) and list it in
[docs/bloem/README.md](docs/bloem/README.md). `contracts/seams.txt` lists the Silo-owned files
Bloem may edit; the count may only go down.

## Multi-repo

Bloem's clients are `bloem-android` (Android phone and TV) and `bloem-apple` (iOS, tvOS, and
macOS) — clean-room projects, not forks of `silo-android` / `silo-apple`. Where `AGENTS.md`
names `silo-android` or `silo-apple` (sibling repos, the client-visible change checklist, API
coordination), read `bloem-android` and `bloem-apple`: follow-up work is done or filed for both,
preferring coordinated multi-repo changes over leaving a platform behind.

## Session and request efficiency

- Keep each agent task bounded to one coherent milestone. Move unrelated follow-up work to a fresh task with a concise handoff.
- Inspect only relevant files, keep tool output focused, and combine safe related operations into one turn.
- Do not spawn subagents unless the user explicitly requests delegation or parallel agent work.
- Avoid repeated polling; use one appropriately bounded wait only when genuinely necessary.
- Run focused verification during implementation and full suites only at an integration or release gate.
- After a major milestone or context compaction, recommend a clean continuation containing the objective, repository/worktree, branch and HEAD, dirty state, completed work, verification, risks, and exact next step.
- In Claude Code, prefer a fresh session after a major milestone instead of repeatedly compacting an extended session.

## Claude Code session efficiency

- Keep this file and auto-memory indexes concise; move detailed discoveries into on-demand topic files.
- Use `/compact` only with a milestone handoff that preserves the objective, repository state, completed work, verification, risks, and next step.
- Prefer starting a fresh Claude Code session after a major milestone instead of repeatedly compacting an extended session.
- Do not create or resume subagents unless the user explicitly requests delegation or parallel agent work.
- Avoid repeated short follow-ups and status polling; complete the approved milestone autonomously and report once useful evidence is available.
