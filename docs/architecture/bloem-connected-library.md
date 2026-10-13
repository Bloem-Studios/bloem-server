# Connected library bridge

The optional installation-bound `bloem.librarybridge.v1.LibraryHost` service
projects catalog inventory, collection metadata and members, explicit owned-copy
records, and edition-bound book progress. Inventory visibility and playable
availability are separate. Digital availability never creates an owned copy.

Reads resolve the current account and profile authority before and after source
callbacks. Cursors bind the source authority, view, query and page size. Pages
expire after at most 120 seconds and contain at most 100 records per family.
Revoked authority prevents publication and reads of previously cached projections.
No file paths, credentials, stream URLs or reader tokens are exported.

Personal collection mutations reuse native ownership and revision checks. Shared
and server collection views are read-only. Copy records remain private to their
account and profile. A mutation's native effect and exact replay receipt commit
in the same transaction; reusing an event ID with a different intent conflicts.

Book states preserve ebook locators and fractional progress separately from
finite audiobook seconds. Edition identity incorporates the authorized physical
file identities and signatures. Mutations reject an edition or file mismatch,
recheck the source revision, and preserve newer native progress. Completion and
reset are explicit operations. Unsupported or ambiguous representations fail
instead of reporting successful conversion.

The companion polls bounded requested-page and mutation queues. Destination
provisioning credentials authorize machine routes; ordinary profile credentials
only authorize profile requests and their results. Connected projections and
confirmed edits do not replace unrelated native lists or tracking state.

Copy and book wire support does not establish complete native companion UI or
all audiobook acceptance cases. Public watch-sync capabilities remain limited
to the separately verified supported operations and media kinds.
