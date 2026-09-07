# Proxy playback auxiliary production

Executor-bound subtitles and font bundles use the API's catalog and object-store
producer while the selected proxy remains the final egress. The proxy must retain
its normal `serve` grant through the body. A separate `auxiliary_transfer` grant
allows API node zero to produce that response; it does not authorize final serving
or execution-node output. Existing `output_transfer` permits are not interchangeable.

The configured proxy opens a response permit bound to the current attempt,
incarnation, owner, epoch, session, plan, transport, executor and selected egress.
API issuance checks that exact binding under registration then attempt locks,
including current source admission and live owner/retention. Successful issuance
commits the aggregate grant deadline before replying. Stop and route retirement
close issuance; final drain includes lost producer grant responses. Permit closure
serializes with issuance on the attempt lock and removes only that response permit.
A failed cleanup does not bypass live grant checks or the final drain deadline.

The internal producer receives the signed immutable recipe in
`X-Silo-Stream-Token` and the permit in `X-Silo-Auxiliary-Transfer`. Neither header is
forwarded to the viewer. It resolves the immutable card and the original requested
file from the committed attempt, acquires API auxiliary authority, and installs a
package-private request context. This context holds a request-local session
projection; it never registers or reconstructs execution, nor pretends the API is
the selected serving node. Shared subtitle identity, requested/effective file,
external/downloaded/embedded format and attached-font validation then runs unchanged.
The font response retains its existing lifetime wrapper through JSON output.

Token proxy subtitle routes delegate to this producer only when executor-bound.
Credential-free `/stream/v3/{session_id}/subtitles/{track}` and `/fonts` routes
require a valid viewer login, the recipe's account, an exact `X-Profile-Id`
selector matching the immutable recipe, and the configured selected egress.
The URL carries no credential; the viewer sends its login credential separately.
The selector is not proof of profile authority: the immutable recipe supplies the
captured profile used by production. Existing signed-token subtitle and font URLs
retain their signed profile and do not require `X-Profile-Id`.

Adoption must verify actual subtitle and font fetches, not just ordinary API
request helpers. Browser media loads cannot assume custom headers; JavaScript
font-bundle fetches must explicitly carry the negotiated credentials and selector
when using the credential-free route. Native clients must accept these exact
session-bound auxiliary paths and pin queries before passing the plan's scoped
headers to their subtitle and font request transports. Publishing these URLs before
that client join is not supported.

The API origin is an explicit constructor input. Userinfo, path prefixes, query
strings and fragments are refused; neither request Host nor a stream token can
choose it. Internal transfers use fresh connections to avoid automatic retry of a
GET after failure on a reused connection. Redirects are refused. Representation
headers and the existing `file_id`, subtitle identity and PGS window selectors are
forwarded; client credentials and internal response headers are not.

Startup must mount `StreamHandler.AuxiliaryProducer` at the internal auxiliary
paths and supply its API-node `ResolveAuxiliary` and `AcquireAuxiliaryTransfer`
callbacks. Proxy startup must pass an operator-configured API origin and selected
proxy `OpenAuxiliaryTransfer` to `WithAuxiliaryProducer`. These construction seams
do not activate a runtime or publish playback artifacts. Initial-flow admission
must keep refusing proxy auxiliary plans until the reviewed wiring and artifact
URL construction are joined. Remux route support remains owned by its separate
producer integration.
