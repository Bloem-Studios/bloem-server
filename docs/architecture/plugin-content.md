# Versioned plugin content

The configured plugin HTTP proxy is mounted at the narrow common parent
`/api/v2/plugin-content`. Pages use `/plugins/{installation_id}/*`; static
assets use `/plugin-assets/{installation_id}/*`. The frozen v1 mounts remain
unchanged. The ordinary public `GET /api/v2/plugin-content/capabilities`
reports revision 1 and whether the proxy dependency is configured. Availability
does not promise that a particular installation or route is available.

The generated OpenAPI extension `x-silo-plugin-content` describes the closed
dynamic mount list. These mounts do not enter native OpenAPI paths or the finite
operation registry. Plugin descriptors and implementations determine methods,
request bodies, statuses and media. The separate finite raw registry retains all
its validation rules. This is a specific plugin-content exclusion, not an escape
hatch for ordinary API operations.

Page mounts accept the same nine HTTP methods as the bridge proxy. The proxy
still matches the plugin descriptor before dispatch and applies its public,
authenticated or admin policy. A page URL without the directory slash receives
a same-origin 308 preserving its query before dispatch, so browser-relative
assets resolve below the installation. This redirect does not authorize content.

Page authorization reuses existing optional session, launch-cookie and unscoped
API-key resolution, including captured user/profile context. Asset authorization
reuses the separate existing session/launch-cookie resolver; asset mounts accept
GET and do not gain API-key access. Static responses retain ServeFile conditions
and ranges. Unsupported mount methods return 405 with Allow.

The existing proxy's credential header filters, response header filters, request
buffering, error behavior and HTTPRoutes RPC remain unchanged. Query forwarding
retains only the first value of each repeated key, as in the bridge proxy. This is not a
websocket tunnel or an arbitrary streaming transport. Bodies are still buffered;
there is no new body limit, durable admission, idempotency promise or automatic
retry policy. Plugin-defined side effects must not be automatically replayed.

Browser adoption requires separate reviewed changes to the launch cookie and
navigation (identity owner) and shared page href producer (operations owner).
The launch cookie's intended path is exactly `/api/v2/plugin-content`, never
`/` or `/api/v2`. This source change does not issue or broaden cookies.

Plugin-generated absolute links, redirects and response bodies are not rewritten.
A preserved v1 absolute href remains a v1 href. Each affected plugin needs explicit
absolute-link and asset/navigation compatibility evidence before consumer
adoption or exclusion ratification. Synthetic proxy tests prove transport and
authorization preservation, not compatibility of deployed plugins. Native clients
do not consume this browser content surface; no native uploader or playback
contract changes. Jellyfin has no corresponding plugin browser mount.
