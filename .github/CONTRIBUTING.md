# Working on Bloem Server

Bloem Server is a public tracking fork of [Silo Server](https://github.com/Silo-Server/silo-server) (see
[FORK.md](../FORK.md)). It is offered without support: issues are turned off and pull requests are not accepted, as the
[front page](README.md) says. You are free to fork it under the AGPL v3. This file is for people working in a fork or
checkout, and for the AI agents that do most of the work here.

A change that belongs in Silo's own scanner, catalog, playback or plugin runtime is better sent to Silo directly, where
it benefits every downstream project. Silo's rules apply there.

## Which repository owns what

This repository owns the backend, the embedded web app, the native APIs, the wiring of Silo's Jellyfin and
Audiobookshelf compatibility servers, the plugin host and Bloem's storage-provider protocol (`proto/bloem/plugin/v1`).
Client-only work belongs in `bloem-apple` or `bloem-android`. The host runs on Silo's public plugin SDK
(`github.com/Silo-Server/silo-plugin-sdk`, pinned in `go.mod`), so a change to the public plugin contract belongs in
Silo's SDK. Bloem's plugin-author SDK is `Bloem-Studios/bloem-plugin-sdk`, and provider behaviour belongs in the
individual plugin repository. Name every affected repository when a change crosses them.

## Using AI well

Most of the code here is written with AI assistance, and that is fine. Submitting output nobody understood is not.

1. Read every line of your diff. If you cannot explain it, do not ship it.
2. Run the tests, and do not trust them blindly: they were also written with AI and have blind spots.
3. Run the app and try the thing you changed.
4. Review your own diff adversarially with whatever tooling you have, and note what it found and what you did about it.
5. Look for AI-introduced bugs: silent behaviour changes, dead code, subtle regressions.

Say what AI was involved in a change you publish: the tool, the exact model ID and the level of involvement.

```md
### AI Disclosure
- Tool(s): e.g. Claude Code, Codex CLI, Cursor — or "none"
- Model(s): exact model ID(s) — or "n/a"
- Involvement: fully AI-generated | AI-assisted | human-written, AI-reviewed | none
- Adversarial review: what your own AI review of the diff found, and how you resolved it
```

## Development setup

See the [development overlay](../docs/bloem/overlays/DEVELOPMENT.md) for setup. The frontend uses pnpm 10.32.1, as
pinned in `web/package.json`; do not substitute Bun commands.

## Validate your change

Run the applicable focused checks while iterating. Before sending a change on, install the documented prerequisites and run the same build, format,
vet, vulnerability, test, lint, and generated-artifact checks that CI uses:

```sh
make build
test -z "$(gofmt -l .)"
go vet ./...
go tool govulncheck ./...
make lint-changed                   # BASE_REF=origin/<pr-base> when not main
make test-go
make test-web
make lint
(cd web && pnpm run format:check)
(cd web && pnpm run budget:check)    # after make build
make verify-settings-bindings-all
make verify-client-dtos
make verify-client-coverage
make verify-client-digest
make verify-bloem-openapi
make verify-seams
make verify-playback-fixtures
make verify-route-inventory
make verify-apiv2-openapi
make verify-apiv2-contract          # BASE_REF=origin/<pr-base> when not main
make verify-apiv2-fixtures
go test -count=1 -run '^TestCommittedArtifactMatchesRouter$' ./internal/apiv2/
make verify-local-paths
```

`make build` installs the pinned frontend dependencies and builds the embedded
web bundle, which `pnpm run budget:check` then measures with Silo's report-only
checker (`web/scripts/check-bundle-budget.mjs`; Bloem has carried no allowance
of its own since October 26, 2026). In CI, the client coverage, client digest and
committed v2 artifact checks also run as Go tests inside `make test-go`, and
`make verify-settings-bindings-all` is split between the contract-check and web
jobs; see [Bloem CI](../docs/bloem/ci.md#jobs) for which job runs what. `make lint` runs the full local Go and frontend linters; CI scopes
its Go lint gate to changed lines because the repository has pre-existing
findings. `go tool govulncheck ./...` is CI's reachable Go vulnerability scan;
CI runs it on exactly the Go patch `go.mod` names, so run it on that patch too.
Touching `internal/apiv2` registrations? Run `make apiv2-openapi` and
`make apiv2-fixtures` and commit what they write; the gates above fail on a
stale artifact or fixture tree.

`make lint` runs `golangci-lint` over the whole tree and reports inherited
findings the repository does not pass yet; CI only gates the lines your branch
changed. `make lint-changed` checks exactly those lines, and it analyzes only
the packages your branch touched, so it takes seconds where a cold run over
`./...` takes minutes of every core. Do not add to the inherited findings.
Never pass `--allow-parallel-runners`: concurrent runs queue behind one
another on purpose.

An upstream merge brings in Silo's own lines, which `make lint-changed` would
report as changed. CI's push gate, `scripts/bloem-lint-changed-ci.sh`, reports
only changed lines that also differ from Silo's copy; run it before pushing a
merge with `BASE_SHA=origin/main scripts/bloem-lint-changed-ci.sh`.

If your change spans this server and Bloem's separately maintained plugin SDK, local iteration through
an untracked `go.work` workspace is expected. Do not rely on that workspace in repo-tracked
config or release pipelines. CI validates this repo with `GOWORK=off`, and any new SDK package
or symbol must come from a pushed, tagged release of the SDK before the change is ready to merge
— the same discipline Silo's own `silo-plugin-sdk` workflow uses, applied to Bloem's
separately maintained SDK.

## Documentation and upstream ownership

GitHub displays `.github/README.md` and uses this file for the repository. Keep Bloem's guide links there and in
`docs/bloem/README.md`, and keep root Silo documents close to upstream. Put additions in Bloem-owned documents or
overlays. Public pages say what is true now, in plain language, with no personal names, private hostnames or addresses. History lives in git.

For a documentation-only change, check relative links and section anchors, `git diff --check`,
`make verify-local-paths` and `make verify-seams`. A full application build is unnecessary unless runtime or generated
contracts changed. If documentation participates in a contract digest, regenerate and review the affected artifact
rather than bypassing its check. Use the [SDK guide](../docs/architecture/bloem-sdk-compatibility.md) when refreshing
plugin protocols, generated contracts or sibling client copies.

## Style

- One thing per change. Do not mix unrelated work.
- Follow existing patterns.
- Comment only what is not obvious.

## For AI agents

Read `AGENTS.md` (`CLAUDE.md` is a symlink to it) for Silo's project instructions, architecture reference and
verification requirements, and [BLOEM_AGENTS.md](../BLOEM_AGENTS.md) for Bloem's overrides, which win where the two
differ. The rules in this file apply to you too: do not ship what you have not understood, and run verification before
declaring work complete.
