# Bloem overlay: Playback protocol v3

Bloem additions and overrides for the upstream Silo document [`docs/architecture/playback-protocol-v3.md`](../../../architecture/playback-protocol-v3.md).
The upstream file is kept verbatim so Silo merges stay clean; where the two
disagree, this overlay describes Bloem Server. Index: [Bloem documentation](../../README.md).

**Location:** section “2.2 `POST /playback/start`”, after the paragraph beginning “A client generates a fresh `playback_attempt_id` per user-initiated playback and…”. **Bloem adds:**

**Accepted feature authority.** Every decision may include
`negotiated_client_features`, the canonical feature set accepted for that
attempt. It is the authority for transport behavior; `server_features` only
describes what the server can offer. Start persists the accepted set, and
replan, idempotent replay, reconstructed responses, and terminal decisions echo
the same attempt-sticky value. Older Silo-compatible clients may omit the new
tokens and ignore this additive response field; they continue to receive signed
legacy media URLs.
