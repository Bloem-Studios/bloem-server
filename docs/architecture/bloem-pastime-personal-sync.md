# Managed Pastime personal synchronization

The existing watch-provider pipeline carries movie and series favourites,
watchlist membership, and whole-star ratings between an enrolled Bloem profile
and its Pastime tracking profile. Playback export remains independent. Custom
lists, owned copies, watchlist ordering, books and audiobooks are outside this
transport.

A Pastime plugin declares `managed_personal_state: true` in its watch-provider
capability metadata only when the coordinated implementation has passed its
integration gate. Enrollment then enables both personal directions and explicit
membership removals. Replaying enrollment converges those settings, including
a capability withdrawal. Older plugins keep personal lanes disabled. Revoking
the grant removes usable credentials and disables personal delivery.

The startup composition wraps only the Pastime provider. Other providers retain
Silo's implementation. Ratings retain Silo's existing three-way reconciliation;
Pastime scores without an exact whole-star equivalent are preserved and reported
as a precision conflict rather than rounded silently.

List reads retain the media identity and originating timestamp on tombstones.
They complete a bounded immutable traversal before publishing any rows. The
Bloem-owned importer holds current enrollment authority, checks the exact
credential, and commits native membership with its reconciliation evidence.
An older peer change cannot overwrite a newer native change; equal timestamps
retain the native state. Failed continuation or revoked authority commits no
partial import. Missing or ambiguous catalog matches preserve existing state
and produce a warning. Absence without an originating removal timestamp is not
interpreted as deletion.

Native list writers acquire their membership row before the evidence trigger.
The importer uses a bounded transaction lock timeout: contention rolls back the
import and leaves the ordinary next synchronization to retry. This avoids
waiting indefinitely while a native writer needs the evidence row.

Native mutations allocate source revisions. Imports retain the peer's originating
time for subsequent conflicts and echo prevention. Replay receipts and ordered
source revisions remain the managed transport's authority. Membership changes
do not record viewing consumptions or change unrelated tracking state.
