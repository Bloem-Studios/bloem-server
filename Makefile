.PHONY: frontend build dev-frontend dev-backend dev-proxy dev-transcode lint test test-go test-web embed-stub clean jellyfin-web migrate-continuum-check verify-local-paths verify-upstream-sync-merge install-hooks migrate-create migrate-validate migrate-status migrate-up migrate-down-to settings-bindings verify-settings-bindings verify-settings-bindings-web verify-settings-bindings-all client-dtos verify-client-dtos playback-fixtures verify-playback-fixtures lifecycle-idempotency-record-client lifecycle-idempotency-status lifecycle-idempotency-finalize

GIT_COMMON_DIR := $(strip $(shell git rev-parse --git-common-dir 2>/dev/null))
MAIN_CHECKOUT_ROOT := $(if $(GIT_COMMON_DIR),$(abspath $(GIT_COMMON_DIR)/..))
SHARED_MAKEFILE_LOCAL := $(if $(GIT_COMMON_DIR),$(abspath $(GIT_COMMON_DIR)/../Makefile.local))
DEFAULT_PLUGIN_SDK_DIR := $(abspath ../silo-plugin-sdk)
SHARED_PLUGIN_SDK_DIR := $(if $(MAIN_CHECKOUT_ROOT),$(abspath $(MAIN_CHECKOUT_ROOT)/../silo-plugin-sdk))
GOOSE := go run github.com/pressly/goose/v3/cmd/goose@v3.27.1
GOOSE_DIR := migrations/sql
ENV_FILE ?= .env

ifneq ($(wildcard $(DEFAULT_PLUGIN_SDK_DIR)),)
DEV_PLUGIN_SDK_DIR ?= $(DEFAULT_PLUGIN_SDK_DIR)
else ifneq ($(wildcard $(SHARED_PLUGIN_SDK_DIR)),)
DEV_PLUGIN_SDK_DIR ?= $(SHARED_PLUGIN_SDK_DIR)
endif

JELLYFIN_WEB_INSTALL_DIR ?= .local/compat/jellyfin-web
JELLYFIN_WEB_VERSION ?= 10.11.6

# Build version stamping: inject the git revision so the admin Build panel shows a
# version even when Go's VCS metadata isn't embedded (mirrors the Dockerfile ldflags).
BUILDINFO_PKG := github.com/Silo-Server/silo-server/internal/buildinfo
BUILD_REVISION ?= $(shell git rev-parse HEAD 2>/dev/null)
BUILD_DIRTY ?= $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo true || echo false)
BUILD_NUMBER ?=
BUILD_DATE ?=
GO_LDFLAGS := -X $(BUILDINFO_PKG).revisionOverride=$(BUILD_REVISION) -X $(BUILDINFO_PKG).dirtyOverride=$(BUILD_DIRTY) -X $(BUILDINFO_PKG).buildNumberOverride=$(BUILD_NUMBER) -X $(BUILDINFO_PKG).builtAtOverride=$(BUILD_DATE)

# Build the frontend (requires pnpm)
frontend:
	cd web && pnpm install --frozen-lockfile && pnpm run build

# Build the Go binary (depends on frontend)
build: frontend
	go build -ldflags "$(GO_LDFLAGS)" -o silo ./cmd/silo/

# Run frontend dev server (proxies API to localhost:8080)
dev-frontend:
	cd web && pnpm run dev

# Run the Go backend (integrated mode)
dev-backend:
	go run ./cmd/silo/

# Run a proxy node (stateless stream proxy, no DB required)
dev-proxy:
	go run ./cmd/silo/ --mode=proxy

# Run a transcode node (HLS transcode worker, no DB required)
dev-transcode:
	go run ./cmd/silo/ --mode=transcode

# Lint Go and frontend code
lint:
	golangci-lint run
	cd web && pnpm run lint

# Frontend test files that fail on main today. This list is shrink-only: delete
# an entry along with its fix, and never extend it to land a change. The Go
# suite has no equivalent — a Go test that cannot pass yet carries a t.Skip and
# its reason in the source, where whoever reads the test finds it.
WEBTEST_KNOWN_FAILURES := \
	--exclude src/pages/Catalog.test.tsx \
	--exclude src/pages/ItemDetail/SeasonContent.test.tsx \
	--exclude src/pages/LibraryRecommended.test.tsx \
	--exclude src/pages/setup-wizard/steps/ServerStorageStep.test.tsx \
	--exclude src/player/hooks/useASSSubtitles.test.tsx \
	--exclude src/utils/storage.test.ts \
	--exclude src/components/admin/AdminContextSwitcher.test.tsx \
	--exclude src/hooks/queries/settingValuesRealtime.test.tsx \
	--exclude src/hooks/useTheme.test.ts \
	--exclude src/hooks/appearanceCacheOwnership.test.tsx \
	--exclude src/contexts/AdminContextProvider.test.tsx

# The Go binary embeds the built frontend, so every Go build and test needs
# web/dist to exist. Tests never serve it, so a placeholder is enough; `make
# build` still builds the real bundle.
embed-stub:
	@mkdir -p web/dist
	@[ -e web/dist/index.html ] || printf '<!doctype html>\n' > web/dist/index.html

# Run the Go and frontend test suites.
test: test-go test-web

test-go: embed-stub
	go test ./...

test-web:
	cd web && pnpm exec vitest run $(WEBTEST_KNOWN_FAILURES)

# Regenerate the settings-contract bindings for every language.
#
# The client repos are siblings of this one (see CLAUDE.md); a missing checkout
# is skipped rather than failing, so a server-only developer can still run this.
#
# The conformance fixture (contracts/settings/v1/conformance.json) travels with
# the bindings: the vendored copy in web/src/lib is what the web runner reads.
# The Kotlin and Swift copies land together with their runners in the client
# repos, which will pick their own test-resource paths.
BLOEM_ANDROID_DIR ?= $(abspath ../bloem-android)
SILO_APPLE_DIR ?= $(abspath ../silo-apple)

settings-bindings:
	@mkdir -p internal/settingskeys
	go run ./cmd/settingsgen -lang go -out internal/settingskeys/keys.go
	gofmt -w internal/settingskeys/keys.go
	go run ./cmd/settingsgen -lang ts -out web/src/lib/settingsContract.ts
	@cd web && pnpm exec prettier --write src/lib/settingsContract.ts >/dev/null
	cp contracts/settings/v1/conformance.json web/src/lib/settingsConformance.json
	@if [ -d "$(BLOEM_ANDROID_DIR)" ]; then \
		go run ./cmd/settingsgen -lang kotlin -package org.bloemserver.bloem.model.settings \
			-out "$(BLOEM_ANDROID_DIR)/core/src/commonMain/kotlin/org/bloemserver/bloem/model/settings/SettingKeys.kt"; \
		echo "wrote Kotlin bindings to $(BLOEM_ANDROID_DIR)"; \
	else \
		echo "skipping Kotlin: $(BLOEM_ANDROID_DIR) not checked out"; \
	fi
	@if [ -d "$(SILO_APPLE_DIR)" ]; then \
		go run ./cmd/settingsgen -lang swift \
			-out "$(SILO_APPLE_DIR)/iosApp/iosApp/Networking/SettingKeys.generated.swift"; \
		echo "wrote Swift bindings to $(SILO_APPLE_DIR)"; \
	else \
		echo "skipping Swift: $(SILO_APPLE_DIR) not checked out"; \
	fi

# Fail when the committed bindings disagree with the manifest, so a manifest
# change cannot merge without regenerating what every client reads.
#
# Split in two because the generated TypeScript is compared after prettier, and
# only the Web CI job has pnpm: the Go job runs this target, the Web job runs
# verify-settings-bindings-web. Locally, `verify-settings-bindings-all` is both.
verify-settings-bindings:
	@CHECK_DIR=$$(mktemp -d) && trap 'rm -rf "$$CHECK_DIR"' EXIT && \
	go run ./cmd/settingsgen -lang go | gofmt > "$$CHECK_DIR/keys.go" && \
	diff -u internal/settingskeys/keys.go "$$CHECK_DIR/keys.go" \
		|| { echo "::error::internal/settingskeys/keys.go is stale; run make settings-bindings"; exit 1; }
	@diff -u web/src/lib/settingsConformance.json contracts/settings/v1/conformance.json \
		|| { echo "::error::web/src/lib/settingsConformance.json is stale; run make settings-bindings"; exit 1; }
	@echo "settings bindings are current"

# The half that needs pnpm: regenerate the web binding, format it the way the
# bindings target does, and compare. Without this a manifest change could merge
# with a stale settingsContract.ts, which is what every web control renders from.
verify-settings-bindings-web:
	@CHECK_DIR=$$(mktemp -d) && trap 'rm -rf "$$CHECK_DIR"' EXIT && \
	go run ./cmd/settingsgen -lang ts -out "$$CHECK_DIR/settingsContract.ts" && \
	cd web && pnpm exec prettier --log-level silent --config .prettierrc \
		--write "$$CHECK_DIR/settingsContract.ts" && cd .. && \
	diff -u web/src/lib/settingsContract.ts "$$CHECK_DIR/settingsContract.ts" \
		|| { echo "::error::web/src/lib/settingsContract.ts is stale; run make settings-bindings"; exit 1; }
	@echo "web settings binding is current"

verify-settings-bindings-all: verify-settings-bindings verify-settings-bindings-web

# Regenerate the client DTO set (Kotlin today) from the registry and the Go
# wire types it roots at. Like the settings bindings, the server commits its
# own copy of the output so the drift check below needs no client checkout;
# when the v3 Android client is checked out as a sibling, the same run also
# copies into its kotlin-generated source directory, the same convenience
# settings-bindings offers.
CLIENT_DTO_OUT := contracts/client/v1/kotlin
BLOEM_ANDROID_DTO_DIR := $(BLOEM_ANDROID_DIR)/core/src/commonMain/kotlin-generated/org/bloemserver/bloem/contract

client-dtos:
	go run ./cmd/clientdtogen -lang kotlin -out $(CLIENT_DTO_OUT) -server-revision $(BUILD_REVISION)
	@if [ -d "$(BLOEM_ANDROID_DIR)" ]; then \
		go run ./cmd/clientdtogen -lang kotlin -out "$(BLOEM_ANDROID_DTO_DIR)" -server-revision $(BUILD_REVISION); \
		echo "wrote generated DTOs to $(BLOEM_ANDROID_DTO_DIR)"; \
	else \
		echo "skipping Android copy: $(BLOEM_ANDROID_DIR) not checked out"; \
	fi

# Fail when the committed client DTOs disagree with the registry or the Go
# types, so a wire-shape change cannot merge without regenerating what every
# client compiles against. The regenerated tree is stamped with the revision
# already recorded in the committed copy rather than HEAD — after any commit
# those two can never match, and the stamp is the only line allowed to differ —
# so a plain diff catches every other byte of drift.
verify-client-dtos:
	@CHECK_DIR=$$(mktemp -d) && trap 'rm -rf "$$CHECK_DIR"' EXIT && \
	STAMPED=$$(sed -n 's/^\/\/ Server revision: \([0-9a-f]\{40\}\) .*/\1/p' "$(CLIENT_DTO_OUT)/GeneratedContract.kt" 2>/dev/null || true) && \
	if [ -z "$$STAMPED" ]; then echo "::error::$(CLIENT_DTO_OUT)/GeneratedContract.kt has no server revision stamp; run make client-dtos"; exit 1; fi && \
	go run ./cmd/clientdtogen -lang kotlin -out "$$CHECK_DIR" -server-revision "$$STAMPED" && \
	diff -ur "$(CLIENT_DTO_OUT)" "$$CHECK_DIR" \
		|| { echo "::error::$(CLIENT_DTO_OUT) is stale; run make client-dtos"; exit 1; }
	@echo "client DTOs are current"

# Regenerate the protocol-v3 golden contract fixtures from the live types and planner.
#
# The server owns the playback contract and the clients prove conformance
# against these bodies, so they are only trustworthy while the code that emits
# them is the code that serves traffic. Editing one by hand instead of running
# this would let the contract and the implementation drift apart in silence.
PLAYBACK_FIXTURE_DIR := internal/playback/testdata/protocol_v3
PLAYBACK_SCHEMA_FIXTURE_DIR := docs/design/schemas/playback-v3/v3/fixtures/valid
PLAYBACK_WIRE_FIXTURES := start_request.json replan_request.json decision_response.json capability_response.json error_response.json route_event.json

playback-fixtures:
	go run ./cmd/playbackfixtures -out $(PLAYBACK_FIXTURE_DIR)
	@set -e; for fixture in $(PLAYBACK_WIRE_FIXTURES); do \
		cp "$(PLAYBACK_FIXTURE_DIR)/$$fixture" "$(PLAYBACK_SCHEMA_FIXTURE_DIR)/$$fixture"; \
	done

# Fail when the committed fixtures disagree with the contract types. A change
# that does not regenerate leaves every client testing against a body the server
# no longer produces, which is exactly the drift the fixtures exist to catch.
verify-playback-fixtures:
	@CHECK_DIR=$$(mktemp -d) && trap 'rm -rf "$$CHECK_DIR"' EXIT && \
	go run ./cmd/playbackfixtures -out "$$CHECK_DIR" && \
	diff -ur $(PLAYBACK_FIXTURE_DIR) "$$CHECK_DIR" \
		|| { echo "::error::$(PLAYBACK_FIXTURE_DIR) is stale; run make playback-fixtures"; exit 1; }; \
	for fixture in $(PLAYBACK_WIRE_FIXTURES); do \
		cmp -s "$$CHECK_DIR/$$fixture" "$(PLAYBACK_SCHEMA_FIXTURE_DIR)/$$fixture" \
			|| { echo "::error::$(PLAYBACK_SCHEMA_FIXTURE_DIR)/$$fixture is stale; run make playback-fixtures"; exit 1; }; \
	done
	@echo "playback fixtures are current"

# Verify the workflow helper cannot merge any head other than the one CI tested.
verify-upstream-sync-merge:
	scripts/verify-upstream-sync-merge_test.sh

# Check committed content for local machine path leaks. This target is part of
# the CI/release gate, so keep the upstream merge fixture attached here too.
verify-local-paths: verify-upstream-sync-merge
	scripts/check-local-path-leaks.sh

# Create a timestamped Goose SQL migration. Usage: make migrate-create NAME=add_thing
migrate-create:
	@if [ -z "$(NAME)" ]; then echo "usage: make migrate-create NAME=add_thing"; exit 1; fi
	$(GOOSE) -dir $(GOOSE_DIR) create "$(NAME)" sql

# Validate Goose migration annotations and SQL parsing without touching a database.
migrate-validate:
	$(GOOSE) -dir $(GOOSE_DIR) validate

# Show Goose migration status through Silo's bootstrapping runner.
migrate-status:
	go run ./cmd/silo/ --env "$(ENV_FILE)" --migrate-status

# Roll back every migration newer than VERSION (the version to KEEP).
#
# Not a routine operation: it discards data. It exists because some migrations
# are Go rather than SQL — the settings backfill and the jellycompat
# DisplayPreferences move — and those are registered in-process, so the goose
# CLI above cannot see or reverse them.
#
# This is a RANGE, not a list: everything newer than VERSION comes off, including
# migrations belonging to other features that happen to sort in between. Check
# `make migrate-status` and read the down of each one you are about to revert.
# Take a backup first regardless; the per-user SQLite stores have no down path.
#
# Usage: make migrate-down-to VERSION=<timestamp from migrate-status>
migrate-down-to:
	@if [ -z "$(VERSION)" ]; then echo "usage: make migrate-down-to VERSION=<timestamp from make migrate-status>"; exit 1; fi
	go run ./cmd/silo/ --env "$(ENV_FILE)" --migrate-down-to "$(VERSION)"

# Apply pending Goose migrations through Silo's bootstrapping runner.
migrate-up:
	go run ./cmd/silo/ --env "$(ENV_FILE)" --migrate-only

# Record immutable proof that one released client passed its lifecycle suite.
# DATABASE_URL must identify the deployment whose rollout is being controlled.
# Usage: make lifecycle-idempotency-record-client CLIENT=web COMMIT_SHA=<git-sha> \
#   SUITE_DIGEST=<sha256> RELEASED_AT=<rfc3339> RELEASE_CHANNEL_DIGEST=<sha256>
lifecycle-idempotency-record-client:
	@for value in CLIENT COMMIT_SHA SUITE_DIGEST RELEASED_AT RELEASE_CHANNEL_DIGEST; do \
		eval 'present=$${'"$$value"'}'; \
		if [ -z "$$present" ]; then echo "$$value is required"; exit 1; fi; \
	done
	go run ./cmd/lifecycleidempotencyctl record-client \
		--client "$(CLIENT)" --commit-sha "$(COMMIT_SHA)" \
		--suite-digest "$(SUITE_DIGEST)" --released-at "$(RELEASED_AT)" \
		--release-channel-digest "$(RELEASE_CHANNEL_DIGEST)"

# Print the rollout phase and immutable client evidence as JSON.
lifecycle-idempotency-status:
	go run ./cmd/lifecycleidempotencyctl status

# Irreversibly require lifecycle idempotency after the command verifies all
# reviewed digests against the compiled route registry and live schema, plus
# the three immutable client evidence records. Obtain current digests from
# lifecycle-idempotency-status, review them independently, then pass them here.
# Usage: make lifecycle-idempotency-finalize CONFIRM=required \
#   EXPECTED_ROUTE_DIGEST=<sha256> EXPECTED_SCHEMA_DIGEST=<sha256> \
#   PRODUCTION_WEB_DIGEST=<sha256>
lifecycle-idempotency-finalize:
	@for value in CONFIRM EXPECTED_ROUTE_DIGEST EXPECTED_SCHEMA_DIGEST PRODUCTION_WEB_DIGEST; do \
		eval 'present=$${'"$$value"'}'; \
		if [ -z "$$present" ]; then echo "$$value is required"; exit 1; fi; \
	done
	go run ./cmd/lifecycleidempotencyctl finalize --confirm "$(CONFIRM)" \
		--expected-route-digest "$(EXPECTED_ROUTE_DIGEST)" \
		--expected-schema-digest "$(EXPECTED_SCHEMA_DIGEST)" \
		--production-web-digest "$(PRODUCTION_WEB_DIGEST)"

# Install repo-local git hooks for this checkout/worktree.
install-hooks:
	@existing="$$(git config --local core.hooksPath 2>/dev/null || true)"; \
	if [ -n "$$existing" ] && [ "$$existing" != ".githooks" ]; then \
		echo "warning: overwriting existing local core.hooksPath ($$existing) with .githooks"; \
	fi
	git config core.hooksPath .githooks

# Fetch and build the pinned Jellyfin Web component into a gitignored local cache.
jellyfin-web:
	go run ./cmd/silo/ compat-web install --dir "$(JELLYFIN_WEB_INSTALL_DIR)" --version "$(JELLYFIN_WEB_VERSION)"

# Read-only preflight for Continuum Docker installs moving to Silo.
migrate-continuum-check:
	scripts/migrate-continuum-docker.sh check

# Clean build artifacts
clean:
	rm -rf web/dist web/node_modules silo

# Include developer-specific targets (gitignored, optional).
# In Git worktrees, fall back to the main checkout's Makefile.local so custom
# targets like dev-deploy work without per-worktree symlinks or copies.
ifneq ($(wildcard Makefile.local),)
include Makefile.local
else ifneq ($(wildcard $(SHARED_MAKEFILE_LOCAL)),)
include $(SHARED_MAKEFILE_LOCAL)
endif
