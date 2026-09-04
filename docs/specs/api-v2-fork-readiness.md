# api-v2 fork readiness

**Status:** ready to execute. Tasks 1 and 2 are unblocked and independent.
**Owner decision pending:** Task 3 only.

## Why this exists

Upstream (`Silo-Server/silo-server`) is migrating its native API to `/api/v2` on Huma, in phases:

| phase | PR | what it is |
|---|---|---|
| 0 | #880 | The program decision, docs only. `/api/v2` becomes the stable 1.0 contract; `/api/v1` is frozen alpha, carried through one bridge release, then `410 Gone`. |
| 1 | #902 | A 762-row route inventory generated from source, a migration ledger, per-route scenario catalogs, and **five new CI gates**. Stacked on #880. |
| 2 | #882 | The Huma foundation. Not yet posted. |

We are a private fork, **509 commits ahead**, tracking upstream fetch-only and merging periodically.
We will inherit all of it.

**The problem this document solves:** phase 1's route-inventory generator is designed to *refuse*
rather than emit a partial artifact, and three constructs in our own routing trigger a refusal. The
four gates that depend on its output are therefore red the moment we merge #902, and stay red until
our code changes. Two of the three are cheap cleanups that are worth doing on their own merits.
Doing them **now**, before the merge, converts a blocked merge into a normal one.

Nothing here requires upstream to change. Nothing here should be pushed to upstream.

---

## Task 1 — stop mounting the compat API under a prefix

**Blocker.** `internal/routeinventory/analyze.go` (upstream branch `api-v2/phase-1-scenario-catalogs`,
~line 1107) refuses outright:

> `Mount() is not modeled by the route inventory; the mounted handler's routes would be invisible.
> Add explicit support before mounting`

**Our violating code:** `internal/api/router.go:2096`

```go
r.Mount("/api/internal/compat/v1", http.StripPrefix("/api/internal/compat/v1", deps.CompatAPIV1))
```

`git grep "\.Mount(" upstream/main -- internal/api` returns nothing. Ours is the only one in the
tree, so this is entirely our problem to remove.

**What to do.** Register the compat API's routes inline on the parent router under the same prefix,
so every route is a literal registration the analyzer can see, instead of a mounted opaque handler.
Do not change any path, method, middleware order or handler behaviour — this is a restructuring of
*how* routes are registered, not *what* is served.

**Self-check (must all pass):**

```sh
# 1. no Mount survives in the API tree
test -z "$(git grep -n '\.Mount(' -- internal/api)" || { echo "FAIL: Mount still present"; exit 1; }

# 2. the served surface is byte-identical before and after.
#    Capture BEFORE you start, on a clean tree:
#      go run ./cmd/route-dump > /tmp/routes-before.txt   # if no such tool exists, write one that
#      walks chi's Routes() and prints "METHOD PATH" sorted; commit it under cmd/ only if useful.
go run ./cmd/route-dump | sort > /tmp/routes-after.txt
diff /tmp/routes-before.txt /tmp/routes-after.txt || { echo "FAIL: served routes changed"; exit 1; }

# 3. the suite
go build ./... && go test ./internal/api/... ./internal/compat/... || exit 1
```

**Do not** delete the compat API or change its prefix. A route disappearing is a far worse outcome
than the gate staying red.

---

## Task 2 — make `mountBloemRoutes` non-variadic

**Blocker.** The analyzer maps call arguments onto parameters positionally and refuses a variadic
signature (~`analyze.go:1013`):

> `cannot map arguments onto %s (variadic or mismatched signature)`

**Our violating code:** `internal/api/router_bloem.go:147`

```go
func mountBloemRoutes(r chi.Router, system *handlers.BloemSystemHandler,
    session *handlers.AdminContextSessionHandler, authMW *apimw.AuthMiddleware,
    adminMW *apimw.AdminContextMiddleware, surfaces ...any)
```

**What to do.** Replace `surfaces ...any` with an explicit parameter — a struct holding the
surfaces, or named parameters if the set is small and stable. Read every call site first; `...any`
usually means the set grew organically, so the struct is likely the honest shape. Type the fields
properly: `any` in a signature is what made this unanalysable in the first place, and a struct of
`any` fields fixes the gate while keeping the disease.

**Self-check:**

```sh
test -z "$(git grep -n 'func mountBloemRoutes' -- internal/api | grep '\.\.\.')" \
  || { echo "FAIL: still variadic"; exit 1; }
go build ./... && go test ./internal/api/... || exit 1
# and the same route-dump diff as Task 1 — the served surface must not move
```

---

## Task 3 — `mountUnavailableAdminContextRoutes` reached twice — OWNER DECISION FIRST

**Blocker.** The analyzer refuses a router-taking helper reached from more than one call site
(~`analyze.go:997`):

> `route registration helper %s is reached more than once; the inventory would duplicate or lose
> its routes`

**Our violating code:** `internal/api/router_bloem.go:186` and `:196` both call
`mountUnavailableAdminContextRoutes(r)` (declared at `:363`).

**Why this one is different.** The two call sites are deliberate degraded-mode branches. Inlining
the helper at both sites satisfies the analyzer but duplicates the route list, and the two copies
will drift — which is exactly the bug the helper exists to prevent.

**Do not start this without a ruling.** Options, for the owner:
- **(a)** Inline at both sites and accept the duplication, with a test asserting the two lists match.
- **(b)** Restructure so the degraded routes are registered once, with the branch deciding the
  *handler* rather than whether to register.
- **(c)** Leave it, carry a local patch to the analyzer's config, and re-apply it on every upstream
  merge.

(b) is most likely right and is the most work. Bring a recommendation before writing code.

---

## Also known, not tasks yet

- **`cmd/silo` root listener will conflict textually.** Upstream #902 extracts the inline
  `metricsMux` block into `newRootHandler`/`newRootMux` in a new `cmd/silo/root_handler.go`, and
  the ABS listener into `cmd/silo/abs_listener.go` as `newAudiobookshelfListener`. We independently
  extracted the same block into `publicServer`/`publicMux`/`servePublic` in `cmd/silo/main.go` and
  added a compat-gateway fallback at `/` that upstream does not have. **Neither side is takeable:**
  theirs drops our gateway, ours fails the generator, and the generator's config hardcodes
  upstream's function names as string constants.
  **Guidance at merge time:** take upstream's file split and re-apply our gateway and `publicPort`
  handling *inside* it, rather than keeping our names. That leaves `internal/routeinventory/config.go`
  untouched, which is the file we least want to own. Our ABS router is still inline at
  `cmd/silo/main.go:3452` and will need the same treatment, or it trips the stray-router audit.
- **The four new gates arrive already-failing** even after Tasks 1–3: the migration ledger wants a
  row per route and we have roughly 372 more registration sites in `internal/api` than upstream
  (1392 vs 1020 verb-call literals), 66 of them in `router_bloem.go`. That is a bulk data task, not
  a code task, and it is best done after phase 2 shows what a v2 row looks like.

## Rules for anyone executing this

1. **Do not change what is served.** Every task here restructures registration. If a path, method,
   middleware or handler behaviour changes, the task is wrong.
2. **Verify with a script, not a claim.** Each task above has a self-check; paste its real output.
   The route-dump diff is the one that matters — it is the only check that proves the surface
   did not move.
3. `go build ./...` and `go test ./internal/api/...` green before you report done.
4. Do not touch `contracts/client/v1/**` — generated, and the owner has uncommitted work there.
5. Do not push to `upstream`; its push URL is disabled and must stay that way.
6. Commit locally on a branch; the coordinator integrates.
