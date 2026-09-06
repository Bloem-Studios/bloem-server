# Executable scenario pairing

Scenario catalogs retain the observed v1 exchange. A non-null `v2_expectation`
records a separate operation ID, method, concrete request, expected status,
headers and body assertions. Its optional principal overrides the v1 principal.
The catalog schema requires these fields; the loader checks the operation,
method and path against the committed OpenAPI document. No URL prefix substitution
or inherited v1 response expectation supplies the v2 contract.

`kind` identifies semantic equivalence or an intentional difference. An intentional
difference requires a summary. `recorded_in` points to the existing repository
contract or test reference supporting the recorded behavior; it is not evidence
that a new external review or approval occurred.

The executor runs both exchanges through the real router and reports stable IDs
such as `profiles_list.shape/v1` and `profiles_list.shape/v2`. Paired transports
start from reseeded fixture state. V1 follow-up steps remain attached to the v1
exchange; the explicit v2 exchange asserts its own response. The current pilot
covers reads, not mutation-effect equivalence.

The required pilot consists of ten cases for `GET /api/v1/profiles/` paired with
`listProfiles` at `GET /api/v2/profiles`. They cover list meaning, shape, ordering,
account isolation, API-key access, missing profile selection, foreign-profile
refusal and missing credentials. Successful v2 responses use `items`, explicit
empty arrays and strings; refusals use Problem Details. Assertions omit volatile
timestamp values and request identifiers. Problem type assertions check the
semantic path; canonical origin checks remain in the v2 contract fixtures.

Run `make test-scenario-profile-pairing` with `SILO_SCENARIO_DATABASE_URL` pointing
to a dedicated empty PostgreSQL database. The executor migrates and truncates
its synthetic fixture tables. Never use a shared integration database. The required
target fails if the database is missing, a fixed pilot case or pairing disappears,
an exchange skips, or any expected status/header/body assertion fails.
`SILO_SCENARIO_REPORT` optionally names the JSON result file.

Ordinary offline unit runs retain optional database execution. Their skipped
cases are not acceptance evidence. The catalog coverage gate still checks all
existing scenarios, but the other unpaired rows remain outside this bounded
acceptance pilot.

## Device-list checkpoint

`make test-scenario-device-pairing` requires the same guarded synthetic PostgreSQL
fixture database. It runs the fixed 13 `devices_list.*` scenarios as 26 independent
v1/v2 exchanges. A missing or duplicate case, cleared pairing, skipped exchange or
failed assertion fails acceptance. `SILO_SCENARIO_REPORT` optionally writes the
per-transport results; use separate report files for the profile and device targets.

The device pairs exercise profile and household visibility, current-device marking,
recency order, empty collections, row fields and authorization/validation failures
through the production router and PostgreSQL provider. V2's `items`/`page` envelope,
canonical UTC timestamps and Problem responses are intentional wire differences.
Uppercase `scope` is a v2 enum validation error (422), while v1 normalizes it before
authorization; a missing profile header is v2 validation 422 versus v1 bad request
400. Neither transport's expectations are inferred from the other's response.

This checkpoint covers the existing single-page fixture, not signed continuation,
timestamp ties, device reset/forget effects, SQLite execution or performance.
Those need separate executable evidence. The existing profile-list target remains
required; passing these two slices does not complete tier-1 migration acceptance.

## Explicit v2 follow-ups

A `v2_expectation` may declare `then` steps with their own operation ID, method,
request and assertions. These steps run against the same state as that transport's
initial exchange. The executor never inherits the legacy `then` list. A failed v2
exchange stops its dependent steps. Existing legacy follow-up behavior is unchanged.

A step's optional `from_previous` array copies a nonempty string from exactly one
header or JSON pointer in the immediately preceding response into exactly one
request destination. Header destinations are restricted to `If-Match` and
`If-None-Match`; query destinations must be declared string parameters of the
step's named OpenAPI operation. For example:

```json
"from_previous": [{"header": "ETag", "request_header": "If-Match"}]
```

Use `{"pointer":"/page/next_cursor","query":"cursor"}` for a continuation.
Captures are applied after fixture substitution and are never interpreted as
another template. They cannot replace the URL path, origin, body, credentials or
profile headers. Explicit fixture principals remain the sole authority selection
mechanism. Static and captured values cannot share a destination. Missing,
ambiguous, empty, non-string or oversized captures fail before the next request.
A previously consumed captured cursor also fails before another dependent request.

Each v2 sequence permits at most 16 physical requests, including declared repeats,
with at most eight captures per follow-up and 16 KiB per captured string. A repeat
retries the same constructed values; it does not consume another cursor. Captures
exist only within that transport's sequence and only the last response feeds the
next step. This is bounded fixture infrastructure, not automatic traversal or a
client retry implementation. Numeric/body/path captures and mutation acceptance
catalog expansion are outside this checkpoint.

## Device mutation effects

`make test-scenario-device-mutations` requires six existing reset/forget cases,
producing 12 transport results. Each v2 sequence explicitly reads the household
list afterward to verify the target effect and preserve sibling devices and
settings. Reset retains the target device with zero changed settings; forget
removes it. Repeated forget returns 404, repeated reset remains 204, and denied
or unknown-target mutations leave the fixture unchanged. These operations do not
support conditional requests, so the cases do not invent ETag preconditions.

Only this required target overlays one canonical setting on each fixture device,
after every reseed and independently for each transport. Teardown reseeds without
the overlay and checks that no profile-device settings remain. The ordinary list
fixture and original v1 records remain unchanged. Missing pairs, missing explicit
read-after vectors, skipped results and failed assertions fail the required gate.
The six pairs issue 22 physical requests including repeats and v2 follow-ups;
result counts describe transports rather than individual HTTP requests.

This checkpoint requires the guarded PostgreSQL scenario database. It does not
claim SQLite coverage, cursor traversal or conditional device mutation support.

## Profile mutation effects and PIN checks

`make test-scenario-profile-mutations` requires eight existing cases: name and
partial preference updates, rejected self-service access changes, deletion and
repeated deletion, protected primary-profile deletion, and correct/incorrect PIN
checks. These produce 16 transport results and 26 physical requests, including
repeated deletion and eight explicit v2 household reads. The reads verify target
changes, sibling preservation and the absence of PINs, hashes and credentials in
profile lists. PIN checks assert credential presence only on success; this slice
does not consume that credential or claim an unlock/expiry lifecycle test.

The v2 update uses PATCH; v1 uses PUT. V2 returns Problems and canonical profile
fields, and PIN checks include `expires_at` (null in this fixture configuration).
Neither update nor deletion supports conditional requests. No ETag preconditions
or dynamic resource-path captures are assumed.

The required target assigns distinct creation timestamps to its fixed synthetic
profiles after each independent transport reseed. This makes positional effect
assertions deterministic without assuming ordering among equal timestamps.
Teardown restores the ordinary fixture. Default fixtures, production behavior
and all original v1 records remain unchanged. Missing pairings/read-after steps,
skipped results and failed assertions fail this gate. SQLite execution, profile
creation, avatars and additional profile authorization cases remain separate work.

## Profile-section reads

`make test-scenario-section-reads` requires the 24 existing read scenarios for
profile overrides, resolved section settings, and the custom-section flag. Each
transport starts from a separate fixture reseed through the real router and
PostgreSQL provider. The gate requires all 48 transport results without skips;
`SILO_SCENARIO_REPORT` writes their individual assertions and outcomes.

The pairs preserve the original v1 exchanges. V2 collections use `items` with
explicit empty arrays and omit `page` for these bounded, unpaginated results. Authorization failures use Problem Details;
missing profile selection and invalid scope/library parameters use validation
422. The server's custom-section flag retains its boolean meaning, including
explicit false and true settings. These cases cover empty home/library pages,
response shape, account isolation, unavailable profiles and missing credentials.

This slice does not establish populated section ordering, override mutation
effects, catalog-media behavior, SQLite parity or full migration acceptance.
Use a dedicated empty database as described above; shared integration databases
are unsuitable because the executor migrates and reseeds synthetic fixture state.

## Profile-section reset effects

`make test-scenario-section-resets` requires the eight existing reset scenarios,
including repeated reset, library scope and authorization refusals. The target
installs a test-only overlay of home and library overrides for two profiles after
every transport reseed. Ordinary read fixtures retain their empty state.

Each v2 reset has four explicit follow-up reads. They verify that the selected
profile/page was cleared on success, other scopes and sibling profiles retain
their rows, and refused resets preserve all four sets. Repeated reset remains
204. The gate requires 16 transport results and issues 50 physical requests,
including repeats and follow-ups. Teardown restores the ordinary fixture and
checks that no section-override settings remain.

V1 requests and assertions remain frozen; their status checks run against the
same populated overlay, while the new effect reads belong explicitly to v2.
This target does not establish concurrent-write isolation, replacement semantics,
SQLite behavior or populated resolved-section ordering.

## Profile-section replacement effects

`make test-scenario-section-replacements` requires the 13 existing replacement
scenarios. Its isolated overlay gives a member's two profiles and a separate
admin account distinct home overrides before each transport. Three explicit v2
reads verify saved fields or empty-set replacement, recipe permission/configuration
refusals, malformed input and profile/account isolation. A successful custom
section retains its recipe/configuration; denied writes retain the original rows.

The gate requires 26 transport results and 66 physical requests, including the
original v1 round-trip and 39 explicit v2 follow-ups. Teardown restores the
ordinary fixture and checks that no section-override settings remain. The v1
oracle stays unchanged. Together with the read and reset targets, this covers
all 45 original profile-section scenarios; it does not establish concurrent
replacement safety, SQLite parity, catalog-media acceptance or full migration
completion.

## New catalog-media read regressions

`make test-scenario-new-catalog-reads` runs 14 **new** v2 scenarios through the
production API router, PostgreSQL catalog/auth/profile providers and scanner file
repository. These cases are separate from the frozen 598-scenario oracle and do
not increase its paired count. The required target fails without its dedicated
database, on a skipped/incomplete run, or on any assertion or cleanup failure.

The fixture contains two libraries, two permitted movies rated PG and R, and a
movie in the denied library. One permitted content item has two allowed file
versions and a third version in the denied library. Each case removes only the
media IDs created by this target, runs the existing scratch-database guard and
household reseed, then inserts fresh media. The guard's refusal of pre-existing
media remains unchanged. Teardown removes the media and verifies the ordinary
scratch guard again; it does not adopt or erase unrelated media.

The cases cover:

- Visible catalog identity/order, exact totals, child-rating filtering, and a
  two-page signed traversal that terminates without duplicates.
- Cursor rejection under another profile (`invalid_cursor`, HTTP 400).
- Content-item detail and the exact permitted file-version set, with string file
  IDs and file paths absent for a viewer without path visibility.
- A foreign `file_id` presentation hint retaining the requested content identity
  and permitted version set; a numeric file ID cannot substitute for a content ID.
- Denied item/version reads, a foreign profile, missing profile selection,
  anonymous requests and unknown items, with explicit Problem assertions.

The target issues 16 physical GET requests and writes a separate report through
`SILO_SCENARIO_REPORT` identifying its new-scenario count, request count and results.
The report complements the test exit status: setup and teardown must also pass.
These are metadata reads with synthetic database records; they do not establish
media-byte delivery, disk-file availability, search-provider behavior, SQLite
parity, concurrent catalog changes, all sorts/filters or performance acceptance.

## New admin catalog source-browse regressions

`make test-scenario-new-catalog-sources` runs 13 **new** v2 scenarios through the
production API router, PostgreSQL account/profile providers and filesystem browse
service. It requires the same exclusive scratch database described above and
reseeds the household before each scenario. These cases are separate from the
frozen 598-scenario oracle and do not increase its paired count.

Each scenario creates a temporary directory containing three directories, a
symlink to one directory, an ordinary file and a broken symlink. The two-page
traversal checks exact ordered directory names, the valid symlink's own path,
parent/path metadata and terminal pagination. Further cases check case-insensitive
prefix filtering, an empty result array, and signed cursor rejection after a
path, prefix, declared-profile or operation change.

Real authorization cases deny an ordinary account's primary profile, an admin
account's secondary profile, a foreign profile and an anonymous caller. Missing
directories and invalid page limits must return the specified Problem status and
code. The required target asserts 13 unique results and 18 physical GET requests;
`SILO_SCENARIO_REPORT` writes their separate new-scenario evidence. Setup, teardown
and the existing scratch guard must pass alongside the report. Temporary files
are removed by the test framework, and the database returns to the ordinary
synthetic household fixture.

This scope establishes local directory browsing and cursor/authorization behavior.
It does not exercise remote object storage, catalog archive import/export,
concurrent filesystem changes, permission-restricted operating-system accounts,
or full migration acceptance.

## New notification inbox regressions

`make test-scenario-new-notification-inbox` runs eight **new** v2 scenarios with
20 HTTP exchanges and five direct PostgreSQL effect reads. It uses the production
API router, notification system and delivery repository, real account/profile
providers, and the migrated inbox timestamp trigger. These cases remain outside
the frozen 598-scenario oracle and do not increase its paired count.

The required target refuses a missing database or pre-existing deliveries/inbox
clocks. Each case cleans only its exact delivery IDs and synthetic profile clock
rows, then runs the ordinary guarded household reseed. Synthetic operational
notifications belong to two profiles on the same account. Notification workers
are not started, and no outbound channels or real delivery destinations are
configured. Teardown restores the ordinary fixture and scratch guard.

The cases check:

- A signed list window excludes arrivals inserted after its first page.
- Read-all applies its captured cutoff, including older rows beyond the displayed
  page, while retaining unread later arrivals and the other profile's delivery.
- Repeated single-item read returns a bodyless 204 without changing `read_at`.
- Foreign delivery reads/writes and cross-profile list/read-all cursors fail
  without altering persisted read state.
- An empty sync checkpoint discovers later arrivals in bounded ordered pages,
  then returns an empty terminal continuation.
- Anonymous and missing-profile requests return their specified Problems.

`SILO_SCENARIO_REPORT` records the unique new cases, HTTP count, effect-read count
and failures. Setup/teardown and test exit must pass alongside that report. This
scope does not repeat the independent reversed-commit ordering tests or establish
websocket/push delivery, retention, concurrent writer behavior, native UI behavior,
or full notification migration acceptance.

### NEW literary administration regression scenarios

`make test-scenario-new-literary-admin` requires the dedicated scratch PostgreSQL
DSN and runs six **new** v2 scenarios, outside the frozen 598-scenario oracle.
The real router uses the production literary service and repository. Each case
reseeds synthetic accounts and two editions sharing a synthetic provider ID;
there are no media files, scans, provider calls or enrollment.

Twelve HTTP exchanges and eight persisted-state reads check existing-work reuse,
work/item-scoped unlink, confirmation and ignore attribution to the acting
account, reverse candidate exclusion after ignore, household-primary and
admin-secondary refusal, and validation/missing-item failures without changes.
State checks include exact edition format, work identity, manual confirmation,
and decision ownership. The fixture refuses pre-existing literary rows before
household reseeding can cascade to decisions, and removes only its exact items
and work between cases and on exit.

`SILO_SCENARIO_REPORT` records unique NEW cases, request/effect counts and failures;
setup, teardown and process exit must pass too. This scope does not establish
concurrent administration, atomic linking plus decision recording, provider
matching quality, client UI behavior or full literary migration acceptance.

### NEW diagnostic download failure scenarios

`make test-scenario-new-diagnostic-download` requires the dedicated scratch
PostgreSQL DSN. Five **new** v2 cases exercise the real router, diagnostic service
and report repository with object storage unconfigured. Seven HTTP requests and
ten full-row snapshot reads verify receiving/failed reports return 409, a ready
report returns 503, an unknown UUID returns 404, and report ownership does not
bypass acting-administrator authorization. The report's ordinary profile, an
administrator's secondary profile and anonymous callers are refused.

Requests include Range, but these failures must remain Problems without download
or redirect headers, stored bucket/key, or manifest content. Persisted report rows
must remain unchanged. A preflight guard refuses existing reports before account
reseeding can cascade through their foreign keys. Each case owns and removes only
one synthetic report UUID. The required test and its cleanup must pass alongside
`SILO_SCENARIO_REPORT`; results remain separate from the frozen 598-scenario oracle.
This scope does not test successful archive streaming, object-store behavior,
capture/upload, retention, browser downloads or complete diagnostics acceptance.

### NEW person curation regression scenarios

`make test-scenario-new-person-curation` requires the dedicated scratch PostgreSQL
DSN and exercises six **new** v2 scenarios through the real router and person
repository. Seven PATCH requests and twelve persisted-row reads check explicit
null preserving values, empty strings clearing dates/text/provider IDs, leap-day
round trips, rejected-date preflight preserving the whole row, acting-admin
refusals and exact missing identity. Successful updates may change `updated_at`;
rejected updates must preserve it along with every other stored field.

Each case reseeds the synthetic household and one synthetic person. Existing
people cause refusal before the fixture takes ownership; cleanup deletes only
its exact ID. Results and counts in `SILO_SCENARIO_REPORT` remain outside the
frozen 598-scenario oracle, and setup/teardown/test exit must pass too. No provider
refresh, concurrent full-row update, client UI or whole curation migration claim
follows from this scope.

### NEW diagnostic history and deletion scenarios

`make test-scenario-new-diagnostic-history` runs five **new** scenarios with the
real router, diagnostic service and PostgreSQL repository. Twelve HTTP requests
and ten full-table snapshot reads check deterministic traversal of equal
microsecond timestamps, cursor limit/filter binding, manifest detail preservation,
exact metadata deletion with repeated 204/absent 404 receipts, and refusal of
ordinary-profile or administrator-secondary deletes. List summaries omit manifests;
all responses omit stored object locations. Remaining reports must stay unchanged.

The fixture reuses the pre-reseed report occupancy guard and owns only three
synthetic UUIDs. Each case gets a fresh synthetic household/report set. Required
DSN, setup/teardown and process exit must pass alongside `SILO_SCENARIO_REPORT`.
These cases are outside the frozen 598-scenario oracle and separate from diagnostic
download-failure acceptance. Object storage is unconfigured: successful metadata
deletion does not establish blob cleanup, durable reconciliation, concurrent-list
snapshot behavior, capture/upload, or native/browser acceptance.

### NEW catalog item curation scenarios

`make test-scenario-new-item-curation` runs six **new** scenarios through the real
router and catalog repository. Seven PATCH requests and twelve full-table
PostgreSQL snapshots check explicit-null preservation, empty text/array/timezone
clearing, exact-item title/year/runtime/genre updates, invalid-timezone preflight,
acting-admin refusal and missing identity. Returned detail must agree with the
mutation; both unrelated items remain unchanged. Successful edits may touch only
the target timestamp, including all-null edits. Title normalization is checked.

The fixture reuses the guarded synthetic catalog, cleans its exact IDs before
each household reseed and refuses existing media before setup. Required DSN,
setup/teardown and process exit must pass alongside `SILO_SCENARIO_REPORT`. These
cases remain outside the frozen 598-scenario oracle. No metadata refresh/provider,
concurrent-update guarantee, season/episode edit or client UI claim follows.

### NEW translation-job history and cancellation scenarios

`make test-scenario-new-translation-jobs` runs six **new** scenarios through the
real router, translation service and PostgreSQL repository. Seven HTTP requests
and twelve full-table job snapshots check the newest-50 list bound and order,
content isolation, empty-array response, refusal of a job under another item's
URL, pending cancellation, completed-job preservation and acting-admin refusal.
Every listed job has the expected string identity; internal request attribution
and deduplication keys stay outside the response. Only the intended pending job
may change status, error message, update timestamp and heartbeat.

The fixture refuses existing translation jobs before router startup recovery or
household reseeding. It seeds completed history and one fresh pending row, then
cleans exact job and catalog IDs before each new case. It never enqueues work or
calls an AI provider. Required DSN, setup/teardown and process exit must pass with
`SILO_SCENARIO_REPORT`. These cases are outside the frozen 598-scenario oracle;
they do not establish cross-node runner interruption, cancellation/publication
races, durable replay, translated output or native/browser acceptance.

### NEW season and episode curation scenarios

`make test-scenario-new-child-curation` runs four **new** scenarios through the
real router and catalog repositories. Six PATCH requests and eight PostgreSQL
snapshots covering all item, season and episode rows check correct child identity,
parent/sibling preservation, episode runtime/leap-day edits, null preservation and
acting-admin refusal. The returned child type, content ID, title and series ID
must agree with the target. Only a successfully edited child's update timestamp
is exempt from the full-row comparison, including null-only edits.

The fixture reuses the catalog occupancy guard. Both child tables require parent
media-item foreign keys, so existing children cannot evade that guard. Exact child
and catalog IDs are removed before each household reseed. Required DSN, cleanup
and process exit must pass alongside `SILO_SCENARIO_REPORT`. These cases remain
outside the frozen 598-scenario oracle and are distinct from movie-only curation
acceptance. No metadata refresh/provider, hierarchy renumbering, concurrent-update
or native/browser behavior is claimed.

### NEW viewer-library discovery scenarios

`make test-scenario-new-viewer-libraries` runs five **new** scenarios through the
real router and library repository. Nine GET requests and ten full-table folder
snapshots check capability availability, enabled-only ordering, account/profile
allowlists, empty access, changed access between requests and unauthenticated
refusal. IDs must be strings. Disabled libraries and internal paths/poster keys
must not leak; reads must leave every stored folder unchanged.

The fixture owns three synthetic folders, reuses the pre-reseed occupancy guard
and deletes only its exact IDs before each new household. Required DSN, cleanup
and process exit must pass alongside `SILO_SCENARIO_REPORT`. These scenarios are
outside the frozen 598-scenario oracle. No object-store presigning, access-policy
mutation endpoint, native UI or concurrent policy-change guarantee is claimed.

### NEW webhook-destination read scenarios

`make test-scenario-new-webhook-destinations` runs four **new** scenarios through
the real router, notification service and PostgreSQL repository. Ten GET requests
and eight full-table snapshots check equal-timestamp ID traversal, disabled-row
visibility, profile/limit-bound cursors, household isolation, an empty administrator
profile and authentication refusal. Public responses include the host and nullable
status timestamps while omitting stored URL/signing fields. All rows stay unchanged.

The fixture refuses existing webhook destinations before setup, cleans only four
synthetic IDs and reseeds the household per case. The notification system is wired
without starting dispatch workers. Stored secret fields contain synthetic opaque
sentinels; this scope tests omission, not encryption or delivery. Required DSN,
cleanup and process exit must pass alongside `SILO_SCENARIO_REPORT`. These cases
remain outside the frozen 598-scenario oracle. No notification sends, provider,
creation/update/delete, concurrent snapshot or native/browser claim follows.

### NEW administrator notification-channel read scenarios

`make test-scenario-new-server-channels` runs four **new** scenarios through the
real router, notification service and PostgreSQL repository. Nine GET requests
and eight full-table snapshots check tied creation-time traversal, disabled rows,
page-size cursor binding, acting-administrator enforcement and the empty collection.
Failure counters/statuses, notification selections and UTC timestamps retain their
stored meaning. Responses omit ciphertext, signing data and internal delivery
watermarks; reads leave the complete stored rows unchanged.

The fixture refuses existing channels before setup and deletes only its three
synthetic IDs before each household reseed. No notification workers start. Opaque
secret sentinels test omission, not encryption or delivery. Required DSN, cleanup
and process exit must pass alongside `SILO_SCENARIO_REPORT`. These cases remain
outside the frozen 598-scenario oracle. No sends, mutation, concurrent snapshot,
native or browser behavior is claimed.

### NEW administrator device-read scenarios

`make test-scenario-new-admin-device-reads` runs five **new** scenarios through
real router, account fanout and PostgreSQL stores. Eleven GET requests and ten
snapshots of all registration, legacy and canonical override rows verify merged
logical override counts, profile metadata, the same device ID under separate
accounts, cursor traversal/binding, missing identities and administrator enforcement.
Canonical-only overrides affect counts while detail retains only the legacy
compatibility settings array. Existing canonical-store metadata has whole-second
precision, represented as UTC milliseconds at the v2 boundary. Reads preserve every
stored row.

Before setup, the fixture refuses existing override rows and registrations outside
its known household seed. Each case reseeds that household and adds one synthetic
registration plus four override rows. Required DSN, cleanup and process exit must
pass alongside `SILO_SCENARIO_REPORT`. These cases remain outside the frozen
598-scenario oracle. No device enrollment endpoint, preference mutation endpoint,
playback command, concurrent snapshot, native or browser behavior is exercised.

### Frozen API-key deletion pairs

`make test-scenario-api-key-deletions` requires seven original `keys_delete.*`
cases: `ok`, `gone`, `other_user`, `bad_id`, `demo`, `shape` and `no_token`.
The fixed selector refuses missing pairings and unsupported sequences. Original
v1 requests and expectations remain unchanged. V2 explicitly records deletion,
repeated/missing refusal, Problem Details and malformed-ID validation 422.

The focused runner uses the original request, principal, settings and expectations
through the real exchange helper. It reseeds before and after every transport,
including originals marked `fresh_state`, and compares complete PostgreSQL API-key
rows before teardown. Fourteen transport results cover sixteen HTTP requests and
28 full-table snapshots. Successful and repeated deletion remove only the exact
member key; refused requests preserve all keys. No asynchronous API-key-auth usage
metadata is exempted because that separate scenario is outside this cohort.

The pre-setup guard refuses non-fixture keys before migrations/reseed. Required
DSN, fixed result inventory, assertions, cleanup and process exit must all pass;
`SILO_SCENARIO_REPORT` records each transport. These are pairs from the frozen
598-scenario oracle, not NEW scenarios. No creation, SQLite, concurrent revocation,
in-flight credential drain or native/browser behavior is claimed.

### Frozen API-key list pairs

`make test-scenario-api-key-lists` requires four original cases:
`keys_list.ok`, `keys_list.sorted`, `keys_list.empty` and `keys_list.no_token`.
Original v1 expectations remain unchanged; v2 records collection envelopes,
string IDs, metadata without reusable secrets, and Problem Details explicitly.
The real router/provider runs eight transport requests with reseeding before
and after each transport and 16 complete API-key-table snapshots. Every row must
remain byte-identical. Required DSN and the pre-setup occupancy guard prevent
silent skips or destructive execution against non-fixture keys. The API-key-auth
usage timestamp case remains outside this cohort; no asynchronous metadata
exception, pagination traversal or concurrent snapshot behavior is claimed.
These are four pairs from the frozen 598, separate from NEW scenarios.

The original `keys_list.meaning` and `keys_list.shape` remain unpaired: their
frozen v1 oracle requires reusable secrets and the old exact field set, while
the current bridge returns metadata with `key_prefix` and `revision`. Their
expectations remain unchanged pending resolution by the contract owner.

### Frozen API-key scope discovery pairs

`make test-scenario-api-key-scopes` requires six original cases:
`scopes.ok`, `scopes.meaning`, `scopes.shape`, `scopes.sorted`,
`scopes.no_token` and `scopes.error_shape`. The original oracle remains unchanged.
V2 explicitly adds the availability flag and Problem Details while retaining the
two scope names, descriptions and fixed order. The real router/provider runs
twelve transport requests with reseeding before and after each transport; all
24 full API-key-table snapshots must prove unchanged rows without exemptions.
Required DSN, the pre-setup occupancy guard and the fixed selector fail closed.
API-key-auth usage metadata remains outside this cohort. These six frozen pairs
are separate from NEW scenarios; no provisioning or scope-enforcement mutation
is exercised.

### Frozen API-key creation refusal pairs

`make test-scenario-api-key-create-refusals` requires five original cases:
`keys_create.bad_scope`, `keys_create.missing_label`, `keys_create.malformed`,
`keys_create.demo` and `keys_create.no_token`. Original requests, settings and
expectations remain unchanged. V2 explicitly records validation 422, malformed
JSON 400 and demo/authentication 403/401 Problem Details. Ten real-router
transport requests use per-transport reseeding and 20 full API-key-table
snapshots to prove all three fixture rows remain unchanged. Required DSN,
pre-setup occupancy and fixed-selector gates fail closed. No successful
credential creation, API-key-auth metadata update or external call is exercised.
These five frozen pairs remain separate from NEW acceptance.
