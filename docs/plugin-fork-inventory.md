# Vondel Private Plugin Inventory

Verified on 2026-08-12. The active private plugin and future catalog scope contains exactly six retained plugins: TMDB, TVDB, Ebook Metadata, Audiobook Metadata, Manga Metadata, and Autoscan ARR.

## Release verification result

All six repositories and Releases are private. Each repository is clean at the exact `origin/main` commit peeled from its annotated release tag, and its exact-head CI and exact-tag release workflow completed successfully. Every Release is published, non-draft, non-prerelease, and contains exactly:

- `plugin-darwin-arm64`
- `plugin-linux-amd64`
- `plugin-linux-arm64`
- `checksums.txt`

Each downloaded `checksums.txt` has a strict three-entry bijection using bare asset names, and all three downloaded binaries verify. The compatible Darwin binary returns the exact plugin ID and version below and self-reports its own downloaded SHA-256. Applicable source, reachable-history, and binary credential-fingerprint scans passed.

| Plugin | Private release | Peeled `origin/main` commit | Exact-head CI | Exact-tag release run | Self-manifest |
| --- | --- | --- | --- | --- | --- |
| TMDB | [`v1.2.23`](https://github.com/Vondel-Media/vondel-plugin-tmdb/releases/tag/v1.2.23) | `9ce9b82ef529aa8112aa814dcc89a422d4443eb0` | [`31612837393`](https://github.com/Vondel-Media/vondel-plugin-tmdb/actions/runs/31612837393) | [`31613669453`](https://github.com/Vondel-Media/vondel-plugin-tmdb/actions/runs/31613669453) | `silo.tmdb` / `1.2.23` |
| TVDB | [`v1.2.27`](https://github.com/Vondel-Media/vondel-plugin-tvdb/releases/tag/v1.2.27) | `a2cc114d7d4c8eaab911a18a289c522813d1ab9d` | [`31615200219`](https://github.com/Vondel-Media/vondel-plugin-tvdb/actions/runs/31615200219) | [`31615507041`](https://github.com/Vondel-Media/vondel-plugin-tvdb/actions/runs/31615507041) | `silo.tvdb` / `1.2.27` |
| Ebook Metadata | [`v0.1.3`](https://github.com/Vondel-Media/vondel-plugin-ebook-metadata/releases/tag/v0.1.3) | `22a1c8b77e2827b77d3ce96144924494548c01d1` | [`31612866065`](https://github.com/Vondel-Media/vondel-plugin-ebook-metadata/actions/runs/31612866065) | [`31613699932`](https://github.com/Vondel-Media/vondel-plugin-ebook-metadata/actions/runs/31613699932) | `silo.ebook-metadata` / `0.1.3` |
| Audiobook Metadata | [`v0.1.6`](https://github.com/Vondel-Media/vondel-plugin-audiobook-metadata/releases/tag/v0.1.6) | `7bc466cec0e455ca63c6aafc13d13cbc531bc623` | [`31612879674`](https://github.com/Vondel-Media/vondel-plugin-audiobook-metadata/actions/runs/31612879674) | [`31613716200`](https://github.com/Vondel-Media/vondel-plugin-audiobook-metadata/actions/runs/31613716200) | `silo.audiobook-metadata` / `0.1.6` |
| Manga Metadata | [`v0.1.3`](https://github.com/Vondel-Media/vondel-plugin-manga-metadata/releases/tag/v0.1.3) | `03fd668f7234cc2624d1d0f6aef98abd66b8f8c5` | [`31612894814`](https://github.com/Vondel-Media/vondel-plugin-manga-metadata/actions/runs/31612894814) | [`31613733260`](https://github.com/Vondel-Media/vondel-plugin-manga-metadata/actions/runs/31613733260) | `silo.manga-metadata` / `0.1.3` |
| Autoscan ARR | [`v0.1.4`](https://github.com/Vondel-Media/vondel-plugin-autoscan-arr/releases/tag/v0.1.4) | `f11998586b51e0c413f82c7d01aa7239d5cb526a` | [`31612907100`](https://github.com/Vondel-Media/vondel-plugin-autoscan-arr/actions/runs/31612907100) | [`31613748655`](https://github.com/Vondel-Media/vondel-plugin-autoscan-arr/actions/runs/31613748655) | `silo.autoscan.arr` / `0.1.4` |

## Verified asset hashes

| Plugin | `plugin-darwin-arm64` | `plugin-linux-amd64` | `plugin-linux-arm64` | `checksums.txt` |
| --- | --- | --- | --- | --- |
| TMDB | `9edfb5c8577da59dae7e6cb653198a42463efe483d16d050579095f92ad83d67` | `52c61dd9dfc8e9f27daebd8d81c5ce1ae77541b43c3881572e59d7a00372f319` | `48065958ffe0fc6c0b674c6a24c8eeb790d0f8dab52ce2a6c7218e7d00603e01` | `8c857e5ad0c1578da05b0197b10035c57433d9386bfed766f213ab459d9380d8` |
| TVDB | `6c558ea7b735063e51d500289b605337e011e7a84d88900941dfe5db41aa84e1` | `353f9df58ed42e39c524faa7deb7d51b4fe58c11819a945ad8fd44dc43a66d11` | `7cbd7ae07f8d6fc9089364732131dc0dc0de6bb88f6cdd5dc8f28672e73a361c` | `a23fc8d78b7a6c838c2a8f11ae4a4c3adba5cab781e7edc604c6268262a2f823` |
| Ebook Metadata | `30763cffd528d8b2187e5155fe8faf8eb51f25069eb403916b1b156dbdc522fd` | `f171c5ebb20f1d8756a7a290c6b42222677b9a7a3f819fb80a2ed99dbd3fd864` | `81da1c08cd67ce67bf814d724ba7e7f16ee100fd2f58b24154fc7840eba39f92` | `49d09d90c95da25d77c13b484a41db7ecb8ec7fdfe093795686d25df9da98802` |
| Audiobook Metadata | `dd03570b6dda52660d6697b80159172634e8c4e56cf780c9f8e91e8e5a428646` | `c9e91fcd3c433669c8b1dc446910dbe403cf70cf9825bba92e7c474c222371c3` | `ed5b1fe9ae172d928ee30c6b9e38de44ec8242fc0a2045d326c69530d99160d7` | `86704fb4c00f00d7c51d781dfba40b8ad03b05a69af281e1f75a1abfce45840f` |
| Manga Metadata | `0cf1bdd48ea0e6aaf3a5bf13ab049dc6f12337f8c9b16c8dd51ce5343bcf5555` | `cbbae95b912c71877771a483cdf33a757be95c1445d206492a5dfa9f0f173819` | `e9e635b40019287a8f0c1855e3876006da656a0c00aee5ef53d063ebe13576ba` | `bbcafc18b1278e525941cb782e32c470c3524bf9e4fb41a916a4a79e29c76631` |
| Autoscan ARR | `8c60acfc01242d815c7ecc98987f998db08c3af2eef834c1a30d9fa6dd30739c` | `8e353a06fc951c21a6573dddfa5db824bf0dcfdc9fc800a687281e0be42b158d` | `e3a1652640dcf0360c6e414c8b1021c21b38ceef3feaa401fca8c838354be2fb` | `e58a3e8383f01ccf61c19b0ba30b1f6b86729d1e6b7f0128e4f64a74eac30323` |

## Provenance and authorization

Every repository has a clean Vondel root with no upstream Git history or tags reachable from it, uses its `github.com/Vondel-Media/...` module path, pins private SDK `v0.13.3` without a `replace` directive, and keeps its upstream remote fetch-only. The common preserved AGPL license file has SHA-256 `be81e1ded9055f0a6c6ea337716140e73fc10a3d18a482f5ade024364cc0f2e0`.

| Plugin | Vondel root | Pinned upstream snapshot | AGPL authorization status |
| --- | --- | --- | --- |
| TMDB | `cad31a3a683b50107a126dd7a252c48527c10cfe` | `Silo-Server/silo-plugin-metadata-tmdb@v1.2.21` (`7bae6ba7d99f49587128dfcb0a56bee6800c6ad0`) | Upstream AGPL-3.0-or-later preserved byte-for-byte |
| TVDB | `084b05a2d2b3d218168e379574e73d9e529f9672` | `Silo-Server/silo-plugin-metadata-tvdb@v1.2.24` (`a04e5a21427e2d9c2f63522603190c81f0017da8`) | Upstream AGPL-3.0-or-later preserved byte-for-byte |
| Ebook Metadata | `5583aaf95fe3690f9be4f32df03da6f276c6688f` | `Silo-Server/silo-plugin-metadata-ebook@v0.1.1` (`70d8acb4dbb68f8cc594099258211cd9a3a3082f`) | Project-owner authorization to copy, modify, and license the Vondel copy under AGPL-3.0-or-later |
| Audiobook Metadata | `5c35b8cf1e6cc56b0f2eb232f0f8e389a5c146ea` | `Silo-Server/silo-plugin-metadata-audiobook@v0.1.4` (`f85c630ecbbe5ef760cae1af4b8fdfb1d666397c`) | Upstream AGPL-3.0-only preserved; release remains under that authorization |
| Manga Metadata | `600c4981452a587359e4f94a431c4ba7843b7a1c` | `Silo-Server/silo-plugin-metadata-manga@v0.1.1` (`1458a58663081bff45da3230f783d55d4955852a`) | Upstream AGPL-3.0-only preserved; MangaBaka data remains CC BY-NC-SA 4.0 |
| Autoscan ARR | `bde63ecf3f9e74d4bb84c696a54edc17b2f101ff` | `Silo-Server/silo-plugin-autoscan-arr@v0.1.2` (`7987ddae852549f5f2ef4e00b6f25dfa5497ddad`) | Project-owner authorization to copy, modify, and license the Vondel copy under AGPL-3.0-or-later |

## Compatibility and security invariants

All manifests retain `silo_api_version: v1` and support Darwin arm64, Linux amd64, and Linux arm64.

| Plugin | Capability and invariant metadata | Preserved configuration |
| --- | --- | --- |
| TMDB | `metadata_provider.v1/tmdb`; movie priority `2`, series/season/episode `3`; `image_resolver.v1/tmdb`, scheme `tmdb`, priority `100` | required secret/password `api_key`, retained only in memory |
| TVDB | `metadata_provider.v1/tvdb`; series/season/episode priority `2`; `image_resolver.v1/tvdb`, scheme `tvdb`, priority `100` | required secret/password `api_key`; bearer token retained only in memory |
| Ebook Metadata | `metadata_provider.v1/ebook-metadata`; ebook priority `2` | `enabled_sources`, `google_books_api_key`, `isbndb_api_key`, `hardcover_api_key`, `default_region` |
| Audiobook Metadata | `metadata_provider.v1/audiobook-metadata`; audiobook priority `2` | none |
| Manga Metadata | `metadata_provider.v1/manga-metadata`; manga priority `1` | `enabled_sources`, `enable_local_dump`, `dump_path`, `dump_refresh_hours`, `enable_anilist_banners` |
| Autoscan ARR | `scan_source.v1/arr` | none |

The known upstream TMDB credential fingerprint (length 32, SHA-256 prefix `b606194c5bac`) and TVDB credential fingerprint (length 36, prefix `883b8232685a`) are absent from current files, reachable histories, and downloaded release binaries. Release and CI workflows use credentialless source checkouts, enforce private visibility and exact annotated tags, and contain no Silo catalog dispatch, public-registry publication, or visibility-to-public operation.

## Next phase

The six verified private Releases are the complete active input to the private catalog and staging materialization work. No catalog entry or public/default server behavior is changed by this inventory. Private GitHub assets must be authenticated during materialization and exposed to the server only through the planned token-free network-local staging catalog.
