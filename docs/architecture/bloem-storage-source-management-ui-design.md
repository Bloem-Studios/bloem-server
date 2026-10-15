# Storage source management UI (S3 and Bookwarehouse) — design

Status: approved by Jim, 2026-10-15. Branch `feat/storage-source-ui-20261015` from `origin/main` 21346650b.

## Problem

An administrator cannot add or manage an S3 storage source from Bloem's web UI. Admin → Plugins →
Storage sources only offers "Install Bookwarehouse" and a Bookwarehouse URL/API-key form. S3 sources
exist only through the multipart admin API (`POST /installations/discover`), so setting one up needs
a hand-built request carrying the 15 MB plugin binary. Source configuration is encrypted and never
returned, so nothing in the UI shows which bucket or prefix a source covers. In production a Movies
library's S3 picker showed "No subfolders. You can use the current folder." at the root; the
configured prefix could not be inspected from the product.

## Goals

1. Add, inspect, test, edit, disable, remove and upgrade storage sources of both kinds (S3,
   Bookwarehouse) from the admin UI. Adding an S3 source is platform-admin only; organization admins
   keep the existing Bookwarehouse flow and see S3 sources only through the library picker.
2. Support any number of S3 sources, one per bucket/prefix/credential set. Each appears separately in
   the library folder picker, so one library can select `/movies`, or `/movies/10s` and `/movies/20s`,
   from one or several sources.
3. Make the library picker explain an empty root and fix the cause of the production empty root.
4. Reuse each plugin's manifest `admin_form` so a new storage plugin needs no new web form.

## Non-goals

- A single S3 source containing several buckets (decided: one source per bucket).
- Changing S3 playback or revision admission (strict versioned vs `checked-etag` stays as documented).
- TV series support for S3 libraries; Bookwarehouse folder API publication.
- Deploying anything. Rollout is a separate, explicitly approved step.

## Decisions (from review with Jim)

- Add via catalog install plus discovery (approach A); no binary upload in the form.
- First version covers full lifecycle (add, edit, test, disable, remove, upgrade).
- In-use S3 sources may rotate credentials only.
- S3 add is platform scope only at launch.

## Design

### Server (Bloem-owned files; shared Silo files touched only for integration points)

1. **Catalog generalization.** `internal/plugins/bloem_storage_catalog.go` accepts one hard-coded
   plugin (`nativeCatalogPlugin = "bloem.storage.bookwarehouse"`). Replace with a small allowlist
   {`bloem.storage.bookwarehouse`, `bloem.storage.s3`}. Every downloaded artifact is still verified
   against the host's immutable approval map by digest; the allowlist is not an approval.
2. **Catalog discover.** New `POST /catalog/installations/discover`, JSON body
   `{artifact_key, provider_source_id, enabled, config}`, served only under the platform scope prefix
   (organization scope returns `404 not_found`; `organization_id` is not accepted). Validation, errors
   (`400 invalid_request`, `422 artifact_rejected`, `503 native_storage_unavailable`,
   `mutation_outcome_unknown`) and atomic publish match the manual `POST /installations/discover`. The
   host downloads the verified artifact instead of receiving a binary. Creates no library, binding or
   scan. `config` must not contain `state_path`. Capability `s3_catalog_discovery` in
   `GET /capabilities`.
3. **Connection summary.** Source documents gain `connection_summary`: an allowlist of non-secret
   plugin fields (S3: `bucket`, `prefix`, `region`, `endpoint_host`, `revision_mode`;
   Bookwarehouse: `base_url_host`). Credentials and the full config are never serialized. The
   allowlist is per plugin id in Go, not derived from the manifest.
4. **Manifest form in artifacts.** `GET /artifacts` entries add `admin_form` and `json_schema` from the
   verified manifest's `global_config_schema` so the UI can render the form without a new fetch.
5. **Credential-only replacement.** `PUT /sources/{source_key}/configuration` today refuses any
   source that backs a library or holds entries. Add a narrowly scoped path for S3 sources: when the
   new config differs from the stored one only in `access_key`, `secret_key` or `session_token`, and
   `source_id`, `bucket`, `prefix`, `endpoint`, `region`, `revision_mode` are byte-identical, allow it
   with the usual `expected_revision` check. Any other difference keeps the current refusal. The
   plugin already preserves entry IDs across credential rotation and invalidates retained cursors.
   Capability `credential_rotation_in_use`.
6. **Test connection.** No new endpoint. "Test" on an existing source calls the existing browse of the
   root; Add is validated by the discovery itself (bad credentials or endpoint fail the install and
   leave nothing behind).
7. OpenAPI documents (`internal/apiv2/bloem_native_storage_document.go`, `contracts/api/bloem/v1`),
   `docs/bloem-api-reference.md` and `docs/architecture/bloem-native-storage.md` are updated.

### Web UI (Admin → Plugins → Storage sources)

- Replace the Bookwarehouse-only `InstallForm` with an **Add source** dialog: choose a plugin from
  the approved artifacts that carry an admin form, then fill the form rendered by the existing
  schema-driven `PluginConfigForm`/`configSchemaAdminForm`. Password fields for keys, `state_path`
  never shown or sent, `source_id` generated, a display name required. Submit calls the catalog
  discover (S3) or catalog install plus connection configure (Bookwarehouse, current behavior). S3 is
  offered only in the platform admin view.
- Source list rows show name, kind, connection summary, enabled/ready state, libraries using it, and
  actions: Test, Edit, Disable/Enable, Remove, Upgrade (existing).
- **Edit.** Unused source: all fields editable (whole-config replacement, existing rule). In-use S3
  source: bucket, prefix, endpoint, region and revision mode are read-only with a one-line reason;
  credential fields are editable and submitted as credential-only replacement. Bookwarehouse keeps its
  current connection form.
- **Remove** confirms the detach semantics ("retained", existing uninstall behavior) and lists the
  libraries affected.
- Several sources are listed side by side; nothing limits the count beyond the host's own limits.

### Library folder picker

- Reproduce "No subfolders" against a fake S3 shaped `movies/10s/…`, `movies/20s/…`, with source
  prefixes `""`, `movies/` and `movies/10s/`, and through a filesystem-gateway style listing (directory
  markers, no `CommonPrefixes`). Fix the cause in the plugin repo if it is the plugin, or in the host
  browse path if it is there; otherwise the production cause is a leaf-level prefix and the fix is
  the summary plus the empty-state text below.
- Empty root text shows the browsed prefix: "This source starts at `movies/10s/` and has no
  subfolders. Use the current folder, or edit the source's prefix."
- Multi-root selection and "Use Current Folder" keep their current behavior.

### Errors and safety

- Secrets are only ever entered in password fields, sent once over the admin API, stored encrypted,
  never returned, never logged. Form state is cleared after submit.
- Failed discovery returns the existing typed errors; the UI maps them to field-level messages
  (credentials rejected, bucket unavailable, endpoint refused, gateway IP not allowed).
- Non-admin rules are inherited from the existing handlers; the new call is covered by the same
  authorization tests.

## Testing

- Go: catalog allowlist and digest-mismatch rejection; catalog discover success, bad credentials,
  unapproved artifact, organization scope refused, `state_path` rejection, unknown-outcome
  reconciliation; `connection_summary` never contains secrets (property test over fixture configs);
  credential-only replacement accepts only the three credential fields; OpenAPI contract drift check.
- Web: unit tests for the manifest-driven form, edit locking, summary rendering, empty-picker text;
  one Playwright journey on a fake S3: add two sources, browse each, select `/movies`, then
  `/movies/10s` and `/movies/20s`, save, reopen.
- Plugin repo (`Bloem-Studios/bloem-storage-s3`): regression test for any listing defect found.
- Catalog repo (`Bloem-Studios/bloem-plugins`): add S3 releases to `plugins.json` as a separate
  change whose digests match the host approval map.

## Rollout (not part of this change)

Publishing the S3 catalog entry, building the host image and deploying to CT135 each need Jim's
explicit approval. Live S3 credentials are entered by Jim through the new form, never by an agent.

## Open items

- Confirm the S3 0.3.2 digest already in the host approval map matches the artifact to be published.

## Resolved

- Organization admins do not get S3 add at launch (platform scope only), per Jim.
