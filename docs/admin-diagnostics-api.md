# Administrator diagnostic downloads

`GET /api/v2/admin/diagnostics/reports/{id}/download` (`downloadAdminDiagnosticReport`)
streams a ready report as `application/gzip`. It requires an authenticated acting
administrator and the selected profile's verification headers. Demo accounts
cannot download reports. The operation returns `Cache-Control: no-store`, an
attachment filename, and a content length when the stored size is known.

The API host opens the stored bundle and streams it directly. It does not return
a presigned storage URL. Range requests receive the entire archive with status
200 and `Accept-Ranges: none`. This preserves the existing web proxy download
behavior. Failures before streaming use v2 Problems: 404 for a missing report or
object, 409 for a report that is not ready, and 503 for unavailable storage or
report services. Interrupted transfers after headers cannot become JSON errors.

The web administrator download helper uses the captured session and profile for
the request and checks that authority again after reading the body. A profile
change prevents creating a browser download from the completed response. The
native Apple and Android clients do not consume this administrator endpoint;
Jellyfin compatibility has no diagnostic administration counterpart.

The frozen v1 download operation retains its existing presigned-URL and proxy
modes during the bridge release. Other diagnostic administration operations
continue migrating separately.
