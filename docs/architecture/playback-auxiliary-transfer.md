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

When initial playback is enabled, `SILO_INITIAL_PLAYBACK_API_ORIGIN` joins the
producer at startup. It is an explicit HTTP(S) origin and requires a restart;
empty configuration keeps proxy auxiliary plans unavailable. API startup mounts
the full internal paths with API-node resolver and auxiliary grant callbacks.
Proxy startup uses its configured node identity to open auxiliary permits. Grant
acquisition, supervisors and permit cleanup participate in application shutdown.
This setting does not enroll a source or activate an attempt.

Published initial and successor plans keep the selected proxy origin and path
prefix, original `file_id`, and subtitle identity selectors. Their auxiliary URLs
carry no bearer or executor token. `Stream.Headers` may carry only the captured
`X-Profile-Id` selector for this join; the client retains the original start
request's bearer in memory and validates the returned origin, path and profile
before applying its scoped headers. It must not recapture ambient credentials
after the response or persist them in the plan.

Credential-free lookup discovers the current activated route from the durable
attempt and resolves its immutable recipe locator. Metadata lookup grants no byte
authority. Missing recipes, retiring or stopped routes, expired authority and
lookup errors never fall back to a mutable legacy grant. Legacy lookup applies
only when the session has no native bound attempt. Final proxy serving and API
production still require their respective live grants.
