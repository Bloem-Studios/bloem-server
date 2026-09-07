# Fenced output transfer

An execution node and the node that sends bytes to the viewer can differ. The
`serve` grant remains exclusive to the selected egress node. An execution node
does not gain that permission merely because it produced the output.

The internal `output_transfer` grant authorizes the execution-to-egress hop. Its
request binds the attempt, incarnation, owner, epoch, executor, session, plan,
transport, execution node, egress node, and one opaque transfer permit. The same
PostgreSQL authority lock and persisted maximum grant deadline used for execution
and serving also cover transfer. Transfer requires active control; preparing,
draining, stopped, expired and mismatched routes refuse issuance and renewal.

## Permit and request sequence

1. The final egress acquires its own serving grant and opens a transfer permit
   through its configured `ExecutorRuntime`. PostgreSQL verifies that this
   runtime's configured node ID is the live route's egress. The runtime never
   accepts a caller-supplied node identity.
2. The permit freezes both node identities and the complete captured authority
   tuple. Its random UUID is sent only on the internal worker request in
   `X-Silo-Output-Transfer`, alongside the existing listener authentication and
   signed executor reference. Neither existing credential substitutes for the
   permit. It is not returned in client URLs, response headers or logs.
3. The worker's configured runtime resolves the candidate permit, then acquires
   an `output_transfer` grant. Issuance compares every permit field with the live
   route under its authority lock. The worker must be the selected execution
   node; the permit must originate from the selected egress.
4. Each response uses a separate runtime grant. Both hop writers check before
   headers and every write, cap socket deadlines by grant validity, and cancel
   blocked writes when authority ends. Transfer permission cannot be used with
   a final-serving writer, or vice versa.
5. The egress closes its permit when the response ends. A lost cleanup leaves
   only a locator, not unbounded authority: issuance still checks the current
   row, lease and route. Attempt deletion cascades retained permits. Deletion
   does not retract an already issued grant; normal stop waits for the common
   durable grant deadline, including transfer grants whose replies were lost.

The permit is a scoped internal capability within the existing trusted
PostgreSQL-connected node deployment. It does not introduce a new node credential
or make arbitrary database clients untrusted. Direct PostgreSQL and immutable
Redis access remain the deployment boundary. The permit cannot be replayed
against a successor or another transport. Reusing it within the same live
response still requires a fresh grant; it does not replay a start or mutation.

## Scope

These primitives alone do not enable distributed initial playback. Worker and
egress startup callbacks, authoritative recipe admission, single-launch remote
orchestration, and guarded proxy delivery must be integrated before that route
can be selected. They do not enable bound progressive remux, legacy ID-only
stop, replacement, takeover, restore, or route fallback.

Focused PostgreSQL/Redis tests verify selected-node and full identity checks,
preparing/draining refusal, permit closure, and cross-attempt substitution. A
real HTTP fixture exercises independently guarded execution-to-egress and
egress-to-client responses through drain. This is evidence for the transfer
contract, not end-to-end playback or deployment acceptance.
