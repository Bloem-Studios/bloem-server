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
