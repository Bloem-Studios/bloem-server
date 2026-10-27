# Bloem presentation evaluators

> **Provenance and status** (checked 2026-10-27 against `main` at `e852f92bf`):
> current. Promotions and seasonal ambience are Bloem additions with no Silo
> counterpart: promotion cards (`internal/promotions`, `3ae12d9f2`) and ambience
> packs (`internal/ambience`, `54ace4675`) both arrived on 2026-09-01, specified
> in the [client engagement spec](../specs/client-engagement.md). On 2026-10-05
> (`85ba110f2`) Bloem moved their evaluation into two bundled worker processes;
> the [cutover receipt](../operations/2026-10-05-presentation-plugin-cutover.md)
> records that step. This page was created by `310c545f9` (2026-10-26), which
> brought evaluation back in-process and replaced the earlier
> `bloem-presentation-plugins.md`. The promoted Home row reaches Silo's sections
> handler through `configureBloemSectionExtensions`
> (`internal/api/router_bloem_engagement.go`). Schedules and delivery are
> described in [seasonal scheduling](seasonal-scheduling.md).

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
