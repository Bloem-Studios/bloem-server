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
