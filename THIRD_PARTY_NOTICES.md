# Third-Party Notices

This repository vendors a small amount of third-party and generated asset
material needed for Silo server builds.

## Jellyfin Web

Silo does not bundle Jellyfin Web in this repository or in the default runtime
image. Administrators can explicitly install Jellyfin Web as a separate
compatibility component with `silo compat-web install` or the admin settings UI.
The installer records the upstream source URL, tag, commit SHA, checksum,
license, and source/provenance metadata beside the installed assets.

## Collection Template Posters

`web/public/images/collection-templates/` contains Silo-generated collection
poster artwork. The artwork is intended to use generic original scenes and
avoid copyrighted movie/show posters, recognizable actors, franchise
characters, provider logos, and readable in-image third-party branding.

## Prairie Server Live TV

Vondel's server-side and web Live TV, OTA tuner, guide, streaming, and DVR
subsystem is adapted from [Prairie Server](https://github.com/Prairie-Server/prairie-server)
at commit `095ecd22fbea3384a905eb9049386015db3ff4d8`, licensed under
AGPL-3.0. Vondel preserves the applicable license and identifies every imported
or adapted source blob in `docs/livetv/prairie-source-manifest.tsv`.

The adaptation changes product/module identity, migration ordering, and shared
Vondel integration points. It does not import Prairie native-client source,
assets, layouts, or tests.
