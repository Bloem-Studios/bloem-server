# Initial playback runtime testing opt-in

Ordinary startup leaves the initial playback runtime off. The application can
assemble it explicitly for testing with these restart-required environment
settings:

```sh
SILO_INITIAL_PLAYBACK_ENABLED=true
SILO_INITIAL_PLAYBACK_RECONCILE_ACCOUNTS=1,2
```

The account IDs are illustrative. Set the reconciliation scope to the actual
accounts in the isolated test instance. The switch accepts only `true`, `false`
or an unset value (off). Enabling requires `integrated` or `api` mode and a list
of distinct positive account IDs, limited to 100 accounts for this testing path.
A reconciliation list without the enable switch is rejected. This is bootstrap
configuration, not a hot-reloaded server setting.

Enabling requires PostgreSQL, Redis, a signing key, the final configured user
store's captured-source interface and the application shutdown registry. Startup
checks PostgreSQL and Redis connectivity and validates the persisted server
installation identity within a ten-second startup budget. It fails startup when
those requirements are unmet. Runtime owner identity is per boot; installation
identity is the persisted identity also used by progress bootstrap.

## Admission and transport limits

The reconciliation list is **not an admission API or an account allowlist**.
Startup never creates source markers or registrations. Each account needs an
independently admitted source with its exact backend, source ID and selection
generation. Ordinary-account enrollment is a separate prerequisite. The synthetic
fixture helper only provisions a fresh isolated account; it must not be used to
enroll existing accounts during application startup.

The switch configures the shared playback handler globally. Unadmitted accounts
report `not_admitted` instead of `not_configured`. Shared start and replay paths
also use the configured runtime, so the switch must not be treated as a way to
preserve legacy playback for other accounts on the same instance. Old attempt
replay and bridge-client coexistence need their own acceptance evidence.

Supported initial execution is direct delivery through the API or encoded HLS
with API execution and API egress. Remote execution, proxy egress, remux and
ordinary-account enrollment are not enabled by this wiring. Routing policies
that select those other paths can refuse initial playback. Capability availability
is not proof that distributed playback or migration release gates have passed.

## Timing and shutdown

The bounded testing policies match the isolated playback harness:

| Supervisor | Maximum duration | Safety margin | Renew before | Poll interval |
| --- | --- | --- | --- | --- |
| Owner | 30 seconds | 1 second | 10 seconds | 10 milliseconds |
| Executor grant | 1 second | 100 milliseconds | 200 milliseconds | 10 milliseconds |

These are testing defaults, not production sizing. Polling is more frequent than
both the safety margin and renewal window; the executor grant is shorter than
the owner budget. Clock and renewal failures revoke local authority. Do not
infer that a deployment's latency, clock behavior or scale meets these budgets
from successful dependency assembly.

Application cancellation fences new initial starts and grants, cancels their
work and joins owner/grant supervisors and retained-owner cleanup. Reconciliation
is registered with the same shutdown completion registry as transcode cleanup.
Each reconciliation tick visits at most one page of 100 intents per configured
account. It does not adopt active owners or invent final stop positions. Missing
source receipts leave uncertain intents durable for later reconciliation.

Shutdown waits under the application's existing graceful-shutdown deadline.
Cancellation is lease loss, not a fabricated stop receipt. If a dependency fails
to honor cancellation and cleanup exceeds the deadline, the application reports
that cleanup did not finish; it must not report a clean terminal receipt.
