# Bloem presentation evaluators

Promotions and seasonal ambience choose what to show with pure evaluators that
run inside the server: `promotions.Engine` and `ambience.Engine`. The server
keeps their database records, administrative CRUD, native API projections and
access authority.

## Host authority

The host loads only conservatively filtered, organization-authorized candidates
and captures a single evaluation instant. It hands the evaluator minimal
presentation facts, not database handles, signing keys, encrypted settings or
stored author metadata. Membership, profile/PIN, library access, dismissal state
and asset access remain host-owned. Returned IDs must belong to the request's
authorized snapshot, in its order; returned content and assets are checked and
rebuilt from that snapshot.

Promotions evaluates candidate selection and delivery separately, preserving
lazy role/membership reads and the distinction between unrestricted libraries
and an explicitly empty allowed-library list. Ambience computes active yearly
occurrences using its embedded timezone data. Host validation constrains those
occurrences without invoking a fallback seasonal selector.

## Runtime

`promotions.NewService` and `ambience.NewService` use the engines.
`NewServiceWithEvaluator` takes any `Evaluator`; tests use it to observe or fail
evaluation. Presentation is optional: an evaluator error omits promotions or
ambience instead of blocking browsing, login or playback, and the host never
logs evaluator payloads. Caller cancellation is propagated.

Until October 26, 2026 the same engines ran in two bundled worker processes
behind a JSON-over-stdio bridge (`internal/bloempresentation`). The workers ran
only this reviewed code, with no operating-system sandbox or resource limits,
so the process boundary added start-up cost and failure modes without isolating
anything the server does not already trust.

## Scope

The shared section integration uses reusable extension hooks with feature
adapters and projections in owned files. Core tenancy, profiles and progress
remain core authority. The seam ledger measures actual upstream-file differences;
moving a service alone is not evidence that a shared seam disappeared.
