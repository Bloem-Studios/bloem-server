# Initial worker preparation and admission

Distributed initial playback freezes execution policy on the selected transcode
node before publishing its immutable recipe. The API host cannot resolve another
node's hardware, device or tone-map filter from its own local configuration.

`POST /transcode/prepare` is an internal node-bearer operation. Without executor
grant and recipe callbacks it returns unavailable. It accepts one bounded recipe
proposal containing the selected node and captured executor identity. The worker
checks that it is the selected execution node, authorizes the input path, and
resolves hardware policy and source validation against its current configuration.
This may run capability probes or source checks; it does not start playback,
claim an output directory, create a session, or acquire execution authority.

The response preserves the complete proposal except resolved hardware backend,
device, software-decode selection, tone-map filter and normalization of an explicit
no-tone-map policy. The caller verifies those limits and rejects missing, foreign
or changed replies. It sends one preparation request to the selected node and
does not follow redirects, replay a lost response, or choose another route.

After source installation, the initial controller must stage the complete route,
write the prepared recipe immutably, and publish its locator before starting the
worker. Preparation itself never performs these control mutations.

`POST /transcode/start` retains its legacy request shape and adds an optional
`executor_recipe_digest`. A bound request must equal the projection of the current
authoritative immutable recipe and carry its SHA-256 digest. That digest covers
the complete JSON recipe, including worker device policy and captured identities.
It is separate from the immutable store's envelope digest. The worker reconstructs
options from authoritative bytes, then acquires execution authority through the
existing grant-enforced launch primitive. It refuses an existing session instead
of replacing it.

Bound starts require readiness and make one launch attempt. They do not retry
with different hardware. A successful response includes the captured executor,
transport ID, recipe digest, effective backend and existing recipe attestations.
The caller rejects empty 202 responses or mismatched receipts, even though the
legacy transport still accepts an empty 202. It does not follow redirects or send
an ID-only DELETE after an uncertain outcome. The initial controller must resolve
that outcome through its captured abort/drain protocol; a worker response is not
a durable replay receipt.

This internal contract supports initial video-encoded HLS preparation and
admission. It does not enable routing by itself. API/proxy egress integration,
startup callbacks and complete initial orchestration remain separate prerequisites.
Remux, audio-only encoding, encoded-HLS replan, track/quality/output changes,
replacement, takeover and restore remain unsupported by this initial path.
