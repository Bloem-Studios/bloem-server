# Audiobook and podcast support

This fork retains native audiobook/podcast behavior and historical database support,
while removing the Audiobookshelf REST/Socket.IO protocol runtime. The old
`service.go`, `abs/`, `abssocket/`, companion listener and credential enrollment
instructions no longer describe executable services.

Native audiobook publication and fixed-file direct playback are described by
[the audio contract](../../docs/architecture/bloem-native-audio-playback.md).
Storage-backed scanning and revision-pinned reads remain host-authorized;
[storage](../../docs/architecture/bloem-native-storage.md) documents source and
location identity. Metadata/provider support does not by itself make an item
playable.

Historical SQL and selected persisted tables remain for upstream Silo backend
switching. They do not mount protocol handlers. See
[protocol removal](../../docs/architecture/bloem-compatibility-removal.md) and
[Silo switching](../../docs/architecture/silo-backend-switching.md) before
changing that persistence boundary.
