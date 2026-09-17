# Bloem media ownership across API replicas

Bloem uses Silo's durable playback attempts and node heartbeats. It does not
introduce a second playback-stop protocol or require sticky API sessions.

## Live TV delivery

A native Live TV session records the API node and process instance that opened
its HLS bridge. An authenticated playlist or segment request received elsewhere
is forwarded once to that process. Both replicas apply the ordinary credential,
Live TV permission, account and profile checks. No peer-only bearer credential
is minted and no browser redirect exposes credentials to another origin.

The destination comes exclusively from a fresh API/integrated heartbeat with
both the recorded node ID and process instance. A reused node name cannot claim
a predecessor's encoder. Configure `NODE_URL` on **each API replica** to its own
HTTP(S) origin reachable by the other replicas, not to a load-balanced service.
The existing hostname/listen-address fallback remains available for single-node
installs. Peer origins cannot contain credentials, paths, queries or fragments.

Forwarding preserves paths, query credentials, range requests and media response
headers. It disables environment proxies and redirects, enforces timeouts and
rejects a second hop. An unavailable or stale owner returns `503` with
`Retry-After: 2`; a released or unauthorized session returns `404`. The API never
silently starts another encoder for the same HLS session. If the owner dies,
the client must open a new Live TV session; existing stale-session cleanup
releases the dead tuner allocation. Forwarding is routing, not encoder failover.

Jellyfin compatibility stream IDs live in PostgreSQL alongside the native
session. Only a SHA-256 digest of the opening compatibility token is stored.
Fetch, reuse and close on another replica require that opener plus the current
native session's account/profile ownership. The tuner URL is resolved from the
native channel state, not accepted from the client. Released sessions cannot be
reused even while their compatibility mapping awaits bounded cleanup. Raw
MPEG-TS viewing renews the native tuner lease without opening an HLS bridge.

Apply the cluster-route migration before deploying the new binary. Sessions
opened by an older binary have no process owner and retain local-only routing;
retune them after a rolling upgrade. The changes do not make an in-flight HLS
encoder survive its owner process.

## Playback stop and capacity

Both native stop entry points use the existing durable attempt stop/receipt
mechanism. The v1 bridge falls back to legacy in-memory behavior only when no
durable attempt exists. A storage error must not be treated as a missing attempt.
Ownership is checked before any stop or history mutation.

The transition to a non-null `playback_v3_attempts.stopped_at` releases Bloem's
fleet capacity reservation transactionally under the same account advisory lock
used for admission. Admission checks the durable stop after obtaining that lock,
so an owner racing to renew or reacquire cannot resurrect a stopped reservation.
The existing durable receipt remains the authority for history deduplication.

The API reconciliation loop batch-checks local producers against stopped
attempts. On its next pass it removes them, interrupts local transports and
runs local-only remote-stop hooks. Those hooks close transcodes and remove local
recipes/grants, but do not create another stop receipt or history row. This
producer cleanup is eventual (normally the 15-second reconciliation interval);
capacity release is immediate when the stop transaction commits. A database
outage delays reconciliation rather than guessing that a session has stopped.
Silo's existing stream-deny and progress-stop checks remain in force.

No native Apple/Android protocol change is required: clients retain their
existing stream URLs, stop calls and status handling. Jellyfin parity is covered
by shared open/fetch/close tests. These changes neither add a new endpoint nor
alter the successful response schema.
