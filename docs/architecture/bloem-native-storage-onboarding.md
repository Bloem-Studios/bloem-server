# Native storage onboarding contract

This document describes Bloem's native storage administration and durable library lifecycle.
It extends the [native storage transport and publication boundary](bloem-native-storage.md).
The surface is experimental; mounted routes and passing synthetic fixtures do not establish
backend or client readiness. This contract follows Bloem
`c87b44545228c93f909275c0b5c4fc1d6a289e1b` (October 7), including Silo through
`74158b4a8`. The [wire reference](../bloem-api-reference.md#native-storage-administration)
lists request fields, response documents, pagination and fixed error codes.

## Authority and routes

Routes live beneath both `/api/bloem/v1/admin/platform/native-storage` and
`/api/bloem/v1/admin/organization/native-storage`. They require the existing signed native
administrative session, current account/session authority and actual resource access.
Platform requests may select an active organization for installation or library creation.
Organization requests derive their organization from the administrative session and reject
an `organization_id` field.

Management transactions retain their actual actor, resource and revision witnesses.
An available shared platform source does not grant an organization authority to configure
or remove that installation. Hidden resources return `404 not_found` before exposing
their native mode or source details. Source metadata omits configuration secrets, executable
paths and archive contents. Responses use `Cache-Control: no-store`.

Paths below are relative to either protected prefix:

| Operation | Method and path | Result after an observed commit |
| --- | --- | --- |
| Inspect capability declarations | GET /capabilities | Explicit capability flags |
| List approved artifacts and visible sources | GET /artifacts; GET /sources | Sanitized authorized results |
| Inspect a source and its bindings | GET /sources/{source_key}; GET /sources/{source_key}/bindings | Current visible identities and revisions |
| Install an approved artifact | POST /installations | 201 with source identity; no implicit library or scan |
| Replace all source configuration | PUT /sources/{source_key}/configuration | 200 with incremented configuration revision |
| Create a pathless ebook library | POST /libraries | 201 with numeric library ID, creation key and revision 1 |
| Recover a library | GET /libraries/{id}; GET /libraries/creation/{key}; GET /libraries | Authorized current state |
| Initialize the same library | POST /libraries/{id}/initialize | 200 with revision 2 and unbound state |
| Bind an initialized library | PUT /sources/{source_key}/bindings/{id} | 200 with binding UUID and library revision 3 |
| Request a full scan | POST /libraries/{id}/scan | 202 with the actual durable queue run ID |
| Disable an installation | POST /installations/{id}/disable | 200 disabled_detached, retained=true |
| Uninstall an installation | DELETE /installations/{id} | 200 uninstalled_detached, retained=true |

Install accepts exactly two multipart parts: `request` and `binary`. Approval comes from
the host's immutable artifact map, not the uploaded manifest. JSON is bounded to 1 MiB;
binary and complete multipart limits are 256 MiB and 258 MiB. Duplicate or unknown fields,
extra parts and trailing JSON are rejected. Library creation supplies no mounted path,
filesystem location, owner UUID or provider URL. Provider-specific connection settings
belong to the encrypted `config` object; they never become library paths or catalog
locations. Configuration values are not returned in source documents.

## Capability declarations

`GET /capabilities` returns `schema: 1`. Support flags become true only when the
actual schema, host/runtime, source/library services, durable queue,
consumer/publisher, authorized reader and finite mutation guards are composed
against the same dependencies. Missing or stopped dependencies keep support false;
mounted routes and artifact approval alone are insufficient.

A ready composition enables `source_management`, `approved_artifact_install`,
`configuration_replace_unbound`, `disable`, `uninstall`, `binding_inspection` and
`binding_mutation`, plus `supported_operations.initialize`, `bind`, `full_scan`,
`source_disable` and `source_uninstall`. `enable`, `retained_namespace_reinstall`
and `backend_verified` remain false, as do `supported_operations.library_update`,
`scoped_scan`, `repair`, `delete` and `unbind`. These protected documents are separate
from the public `/api/bloem/v1/capabilities` feature-token probe. Library reads also
return state-sensitive `supported_operations` and `ready_to_queue`; neither an available
route nor global support grants authority to mutate a particular library.

## Stable library and source identities

The library progresses through three durable revisions:

- L1: the canonical pathless ebook folder, immutable native marker, creation key and
  collection group exist; initialization is required.
- L2: serialized library and home sections have been seeded and their retained witnesses
  verified; the same library is initialized and unbound.
- L3: an authorized transaction has committed the unique retained binding.

Initialization may commit earlier section stages before a later stage fails. Recovery
reuses the same library ID and creation key, preserves custom sections and never creates
a replacement folder. An initialized unbound L2 library permits the exact L1 retry.
Binding requires the current source revision S and library revision L; an exact retained
L3 binding permits the L2 retry and returns `repeated=true`. This does not allow a stale
source revision, a different binding or a detached source.

A full scan requires current S and L. It reuses the existing queue service and returns
the actual durable accepted/running run with `created=false` on coalescing. An accepted
run will see the new request. A running run may already have visited its scope, so
coalescing records one owed follow-up with the native scan trigger; completion or
failure atomically enqueues that follow-up. Multiple requests share the same owed
follow-up. A completed run allows a new request. `scan.accepted` is emitted only for
a newly created run after an observed commit. A `202` acknowledges admission, not
completed ingestion. Native execution participates in the host's cancellable overlap
waiting and releases its claim when it finishes. Library revision does not advance on
reads, scan requests or exact read-only retries.

Source revision and library revision are independent. Configuration replacement and
disable/uninstall fence affected source generations and claims. Disable/uninstall detach
the installation, retain its last installation identity, advance S and preserve the source,
library, binding, catalog item/file/reference IDs and reader progress. Future scan and
reader admission fails without falling back to local filesystem access. Local runtime
cancellation and reaping are not a claim that streams on other server nodes have stopped.

Configuration replacement refuses a namespace containing any sibling binding, entry or
reference with `409 configuration_namespace_unverified`. Reinstalling a detached source
requires the same retained owner/plugin/provider/root identity and an empty retained
namespace; otherwise it returns `409 retained_namespace_unverified`. A detached source
whose installation lineage cannot be established is refused. Retention does not imply
that a populated detached source can currently be reattached.

## Failure and recovery

Revision conflicts return `409 revision_conflict` with the applicable current revision.
Malformed requests return 400, oversized requests 413 and rejected artifacts 422.
Missing dependencies, inconsistent state and unavailable sources return the fixed
`503 native_storage_unavailable` response.

An initialization-stage failure that can be reconciled returns
`503 initialization_incomplete` with the authorized library ID, creation key, revision
and state. A commit whose outcome cannot be acknowledged returns
`503 mutation_outcome_unknown`, an operation ID and the recovery identifiers known
after authorization: library ID/creation key, source key or scan run ID as applicable.
A confirmed rollback is distinct from an unknown outcome. Neither an uncertain
acknowledgement nor a callback completing constitutes successful acceptance.

Recover a lost create response through the authorized library list or ID/creation-key
lookup. Retry initialization on that same ID and current state. Reconcile a binding
through its retained binding and source/library revisions. Reconcile a scan through its
known run and active scope before explicitly requesting another. Do not automatically
allocate another library or remove installation bytes after an uncertain commit.
Native-storage mutations are outside the shared lifecycle-idempotency route registry;
its feature token does not authorize automatic replay of these operations.

## Publication and compatibility boundaries

Native EPUB/PDF parsing and cover I/O occur outside retained catalog locks. Actual
publication rechecks authority, source revision, generation, lease, pending claim and
selected entry in the publication transaction. Catalog rows, native references and
claim progress commit together. Failed publication retains retryable pending state.

Ordinary local mutations retain the existing host authorization model. Native
management/publication have the stronger retained transactional checks described above;
they do not establish a global SQL or policy-generation lease for unrelated local work.
Finite guards cover selected ordinary v1/v2 library, scan, repair, metadata, image,
translation and trailer operations. They authorize the complete selected item/file/library
set before classifying native mode, including mixed local/native targets. Hidden resources
and missing PIN authority retain their ordinary refusal precedence. Authorized native
targets return `409 native_library_delete_unsupported`, `native_repair_unsupported` or
`native_local_operation_unsupported`, according to the operation. Unavailable or
inconsistent classification returns `503 native_storage_unavailable`; v2 preserves these
codes in its problem responses.

Provider lookup may precede discovering a target, but fresh host checks authorize each
late-selected target before its mutation. A denied phase has no effect even if lookup
consumed quota; earlier authorized commits remain committed. Request continuations retain
the originating phase checks within the existing lifecycle bounds. Trusted durable jobs
and native enrichment keep their existing host authority without an HTTP request origin.

The reader uses the existing authenticated EPUB/PDF operations. Native locations are not
accepted as ordinary local downloads, repair, conversion, unbind or delete operations.
Backend verification, process-restart acceptance and native-client integration remain
separate requirements. `backend_verified` remains
false; no production backend is approved by synthetic provider tests.
