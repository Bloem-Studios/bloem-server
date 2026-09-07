# Bound playback route replacement storage

The storage protocol stages a successor executor within an existing active
playback attempt. It preserves the captured source/admission, activation,
sink fence, progress sequence and retention deadline. It does not enroll a new
source, install another sink, take over an expired owner, or expose a new HTTP
operation.

The gated initial-playback runtime uses these primitives for captured seek
reanchors. Worker preparation, candidate execute grants, exact-namespace cleanup
and client publication remain separate from the storage transaction.

## Captured replacement

A bounded `route_replacement` document lives on the existing replan row. It
captures the replan request/digest/lease/base revision, expected predecessor
plan/route/recipe locator, successor plan/recipe/route/locator and exact response.
A changed executor requires a fresh executor namespace and transport identity.
The successor retains the same owner incarnation/epoch and logical attempt.

Mutations lock registration, attempt and replan in that order. They require the
exact captured initial binding, current admitting registration and live active
owner. Stage compares the current base, route and locator, account/profile,
original requested file and request digest. It also preserves the progress mode
and any captured client timeline; a replacement cannot silently change parts
under an immutable timeline binding.

Stage and read do not grant execution or serving. A trusted preparation caller
acknowledges the exact candidate route/locator with an opaque receipt identity.
It must validate the worker's readiness receipt before calling storage. The
worker's complete recipe-card digest and the immutable locator's envelope digest
are distinct values; equality between them is not readiness evidence.

## Phases and retirement

The phases are `staged`, `ready`, `retiring`, `committed` and `cancelled`. Only one
staged/ready/retiring replacement may exist per session. Cancellation is allowed
before retirement and retains the candidate and its conservative cleanup
barrier. The predecessor remains current. After retirement, there is no automatic
predecessor revival or new attempt to escape uncertainty.

Beginning retirement atomically sets `control_retiring_replan` to the exact
request ID and clears both current route and current recipe locator. The
replacement document retains both generations' routes and locators. Existing
grant predicates consequently refuse predecessor issuance. The attempt remains
active, its old plan projection remains readable, and its sink is not stopped.
Database constraints permit an outstanding grant deadline without a current
route only when the explicit retirement marker is present.

Retirement freezes `DrainNotBefore` from database time and the durable aggregate
grant maximum. This conservatively includes grants whose replies were lost.
Candidate execute grants may continue during staged/ready/retiring phases only
under the same live active authority; runtime integration must prohibit
candidate serving and output-transfer authority before commit. Candidate renewals
may increase the aggregate after retirement, without moving the frozen
predecessor barrier. Final stop always uses the aggregate covering both routes.

After the frozen barrier passes, cutover atomically changes plan, effective file,
recipe, normalized request, stored decision, current replan revision, route and
locator. It clears the retirement marker without changing activation or retention.
An uncertain commit is resolved by reading/repeating that exact captured key.
A committed replay is actionable only while that revision remains current.
No process or network operation runs inside the database transaction.

## Retention and stop

Replan release cannot delete a replacement document. Expired lease acquisition
cannot replace its retained lease, and the older projection-only completion
cannot bypass a pending replacement. Cancelled records remain available for
conditional cleanup through the attempt's retention. Exact reads remain
available after owner expiry or stop; they confer no execution authority.

A fresh explicit seek can proceed after cancellation only when
`ConfirmBoundRouteCancellation` verifies the retained phase and frozen barrier
against database time under the captured live binding. The runtime then closes
only the cancelled candidate pointer and clears its pending blocker. It retains
the old key for the attempt owner lifetime: retrying that key cannot launch or
clean a later namespace. An uncertain confirmation keeps the blocker.

Terminal stop closes issuance through the existing attempt state and retains the
aggregate deadline. It can run while current route/locator are absent. Both
executor identities remain available from the replacement document for exact
cleanup. Cutover after stop refuses. The existing sink terminal receipt is used
once; no predecessor exit or cleanup callback may stop a successor by a bare
session or transport ID.

The storage barrier proves expiry of previously issued grants, not worker exit,
media availability or UI behavior. Runtime integration must verify preparation,
maintain candidate execution across retirement, clean exact namespaces, publish
local projections conditionally and return playable responses only when their
producer is available. Source tests do not establish those runtime properties.
