# Playback overlays API

See [the playback overlay contract](architecture/playback-overlays.md) for delivery,
capability detection, placement fields, client lifetime rules and the authenticated
save-to-inbox endpoint. Garden uses the existing `/api/v1/admin/promotions` CRUD API.
The embedded web console uses `/api/bloem/v1/admin/platform/promotions` under native
platform authority, reusing the registry without revision locking. Campaign artwork
uploads use the ambience asset service and require public S3; see the
[native authoring contract](bloem-api-reference.md#platform-campaign-and-seasonal-authoring).
Server administrators retain the independent basic announcements UI.
