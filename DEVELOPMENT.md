# Developing Bloem Server

How to build, run, and test Bloem Server from source. To run Bloem without
building it, see the [README](README.md). Contribution rules and the
pre-submission gate are in [CONTRIBUTING.md](CONTRIBUTING.md).

## Prerequisites

- Git, Make, and OpenSSL
- Docker Engine or Docker Desktop with Docker Compose 2.24+ (local services and testcontainers)
- Go 1.26.4+
- Node.js 22+ with pnpm 10.32.1. The managed Jellyfin Web installer (`make jellyfin-web`,
  `silo compat-web install`, or the admin settings) needs Node.js 24+ for Jellyfin Web 12.x.
- PostgreSQL 18 with pgvector
- Redis
- FFmpeg (transcoding). On macOS, install Homebrew's keg-only full build so
  text-subtitle burn-in and the complete filter set are available alongside
  VideoToolbox: `brew install ffmpeg-full`. Silo discovers the Apple Silicon
  and Intel keg paths automatically when the FFmpeg Path setting is blank; a
  custom build can be selected explicitly in Admin Settings.
- A C compiler and build toolchain (CGO dependencies)
- pkg-config and the libvips development headers (image processing through bimg)

## Local development

Source builds use [docker-compose.yml](docker-compose.yml) only for PostgreSQL
and Redis; the deploy-oriented stack in the README is separate.

```sh
# Create the local bootstrap configuration
cp .env.example .env
chmod 600 .env
printf '\nSECRET_KEY=%s\nDATABASE_URL=%s\nREDIS_URL=%s\n' \
  "$(openssl rand -base64 48)" \
  'postgres://silo:silo@localhost:5432/silo?sslmode=disable' \
  'redis://localhost:6379' >> .env

# Start local PostgreSQL and Redis
docker compose up -d postgres redis

# Install frontend dependencies and create the embedded-frontend test stub
cd web
pnpm install --frozen-lockfile
cd ..
make embed-stub
```

Run the backend and frontend in separate terminals, backend first:

```sh
make dev-backend
```

The source-built backend listens on `:8080`, while the Vite proxy defaults to
the Compose port `8090`. Point it at the source backend in `web/.env.local`:

```dotenv
VITE_API_PROXY_TARGET=http://localhost:8080
```

Then:

```sh
make dev-frontend
```

`.env.example` ships a non-empty `MEDIA_ROOT` because Compose validates the
whole file even when you only start PostgreSQL and Redis. Change it before
testing libraries against real media.

### Working on the plugin SDK at the same time

If a change spans Bloem Server and `silo-plugin-sdk`, use an untracked local `go.work`
workspace. `go.work` and `go.work.sum` are gitignored developer conveniences: CI
runs from a clean checkout without them, and release builds set `GOWORK=off`.
Any SDK package or symbol this repository uses must therefore be pushed and
tagged in `silo-plugin-sdk` before the change here can merge.

Plugin authors should start in the `silo-plugin-sdk` repository, usually checked
out beside this one. It owns the plugin package format, protobuf contracts,
generated plugin API, import paths, and manifest helpers.

## Build and run from source

With `.env` created and PostgreSQL and Redis running:

```sh
make build
./silo
```

The server listens at <http://localhost:8080>. Complete onboarding and manage
the remaining settings in the web interface.

## Make targets

| Target | Description |
|---|---|
| `make build` | Build frontend + Go binary |
| `make frontend` | Build frontend only |
| `make dev-frontend` | Vite dev server with HMR |
| `make dev-backend` | Run Go backend (integrated mode) |
| `make dev-proxy` | Run a standalone proxy node |
| `make dev-transcode` | Run a standalone transcode node |
| `make migrate-create NAME=add_thing` | Create a timestamped Goose SQL migration |
| `make migrate-validate` | Validate Goose migration files without touching a database |
| `make migrate-status` | Show Goose migration status using Bloem's bootstrapping runner |
| `make migrate-up` | Apply pending Goose migrations using Bloem's bootstrapping runner |
| `make clean` | Remove build artifacts |

## Database migrations

Goose manages the PostgreSQL schema. Migration SQL lives in `migrations/sql/`
with Goose annotations. Create new migrations with timestamped filenames:

```sh
make migrate-create NAME=add_thing
make migrate-validate
```

Never run `goose fix`. Timestamped names avoid version collisions across
parallel PRs; the `001`-style files are converted legacy migrations that keep
their original numbers so existing `schema_versions` rows bootstrap into Goose
without replaying old SQL. Do not renumber them.

Only the integrated/API server applies migrations at runtime. Proxy and
transcode modes never touch the schema.

For existing installs, use `make migrate-status` and `make migrate-up` rather
than the Goose CLI: those targets copy legacy `schema_versions` rows into
`public.goose_db_version` under the migration lock before reading or applying
anything. Set `ENV_FILE=path/to/.env` to read the database URL from a different
env file.

## Tests and lint

While iterating:

```sh
GOMAXPROCS=2 GOFLAGS=-p=2 GOWORK=off go test ./internal/<package>/...
cd web && pnpm exec vitest run path/to/test.tsx
```

Run focused checks serially on constrained development hosts. Database-backed
tests need disposable PostgreSQL: set `SILO_TEST_DATABASE_URL` and
`SILO_REQUIRE_TEST_DATABASE=1` for required integration checks. Some fixtures create
and drop their own fully migrated child databases; the test account needs those
permissions. Never repair a fixture by changing public tables in an existing database.

Scenario execution uses a separate `SILO_SCENARIO_DATABASE_URL` pointing to an owned
scratch database and `SILO_SCENARIO_REQUIRED=1` for required runs. Lifecycle routes
require a reachable phase store even for malformed-input cases; a missing prerequisite
is an explicit failure. See [scenario execution](docs/architecture/scenario-acceptance.md)
for reset guards, scoped avatar storage and per-transport reporting. The ordinary
Bloem gate applies [explicit downstream adjudications](docs/architecture/bloem-contract-adjudications.md);
historical Silo selectors keep their original expectations.

`make test-go` sets a 20-minute per-package timeout. Remote CI for deployed revision
`418a18b7d` still exceeded that bound in the executor; the 189 other package passes
are not full-suite success. Its [deployment exception](docs/operations/2026-09-19-xtream-deployment.md#ci-result-and-approved-exception)
was specific to that revision and run, not a testing-policy exemption.

The [web coverage matrix](docs/architecture/bloem-web-feature-coverage.md) and
[completion handoff](docs/architecture/bloem-web-completion-handoff.md) distinguish
automated checks, browser acceptance, the completed deployment and remaining media
acceptance. Preserve the existing web exclusions; do not add skips or weaken
assertions to conceal failures.
For documentation-only changes, check changed links/anchors, `git diff --check` and
`make verify-local-paths`; a new full build or test suite is unnecessary.

Before running or deploying a new binary, verify its platform and `go version -m`
VCS revision/dirty flag against the checkout that supplied the source. A successful
build or a healthy older process does not establish that the new artifact was tested.
In worktrees, check for parent-checkout VCS metadata before trusting the stamp.

The full pre-submission gate (build, format, vet, lint, both test suites, and
the verify targets) is listed once, in
[CONTRIBUTING.md](CONTRIBUTING.md#validate-your-change).

## Project structure

```
cmd/silo/       Entry point
internal/
  api/               HTTP router, handlers, middleware
  auth/              JWT authentication and sessions
  catalog/           Media item, episode, season repositories
  config/            YAML + env var configuration
  jellycompat/       Jellyfin/Emby protocol compatibility
  metadata/          Plugin-driven metadata matching and enrichment
  playback/          Direct play, remux, transcode session management
  scanner/           Media file discovery and FFProbe
  worker/            Background jobs (scan, match, reconcile)
web/                 React + TypeScript frontend (Vite, Tailwind, shadcn/ui)
migrations/sql/      Goose-managed PostgreSQL schema migrations
```
