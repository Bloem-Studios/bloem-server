# Native raw HTTP handshakes

`apiv2.RegisterRaw` registers finite GET/HEAD byte-stream handshakes alongside
Huma operations in the same native OpenAPI document. Each declaration supplies
its protocol, reason for raw handling, path parameters, response statuses, and
media types. Binary payloads use their actual media type and a binary string
schema. JSON success responses use the structured `Register` API.

Raw operations use the declared account/profile/administrator authorization
class. Missing gate dependencies return a problem before the transport runs.
The adapter transfers the authorized context into the HTTP request, including
account and acting-profile identity. The owning transport remains responsible
for its resource ownership, media grants, executor namespace, and declared
path/query/header validation. Registering an authenticated stream does not
replace these resource checks.

The shared structured-response buffer runs only on Huma operations. Raw handlers
receive the streaming writer, including `http.ResponseController` access to flush
and connection controls. They own byte ranges, HEAD behavior, conditional headers,
content encoding, redirects, and failures after headers are committed. They do
not inherit JSON Accept negotiation or Huma input validation. Shared request IDs,
operation observation, authorization gates, and method/Allow registration remain.

This initial registration API covers GET and HEAD delivery. Structured body,
concurrency, and deprecation options are rejected: protocols document and enforce
their own controls. Mutation handshakes and WebSocket upgrades require their own
concrete extension and protocol tests. Dynamic plugin schemas and root operator
probes remain explicit exclusions; neither becomes a fabricated JSON operation.

The legacy `RawHandshakes` exclusion list remains separate. A finite raw operation
already described in OpenAPI must not be added to that list, because reconciliation
rejects double accounting. Domain registration belongs in `registerAll` and runs
for both live routers and deterministic contract generation, even when its runtime
service is unavailable. An unavailable service supplies a handler that fails closed;
it does not omit the operation.
