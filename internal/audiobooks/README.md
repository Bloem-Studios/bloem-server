# Audiobook and podcast support

This fork keeps native audiobook/podcast behavior and runs Silo's
Audiobookshelf-compatible REST/Socket.IO server (`service.go`, `abs/`,
`abssocket/`) on its own port. Bloem's companion credential enrollment is removed.

Native audiobook publication and fixed-file direct playback are described by
[the audio contract](../../docs/architecture/bloem-native-audio-playback.md).
Storage-backed scanning and revision-pinned reads remain host-authorized;
[storage](../../docs/architecture/bloem-native-storage.md) documents source and
location identity. Metadata/provider support does not by itself make an item
playable.

The Audiobookshelf server's scope resolver is tenant-aware; see
[protocol compatibility](../../docs/architecture/bloem-compatibility-removal.md) and
[Silo switching](../../docs/architecture/silo-backend-switching.md) before
changing that persistence boundary.
