# Downloaded subtitle storage

Downloaded subtitle content has separate logical and physical identities. The
logical identity is the media file, provider, language, format, and full SHA-256
of the bytes. PostgreSQL enforces uniqueness for rows with a known digest. Every
new publication uses a fresh UUID object key; keys never move when metadata
changes and are never reused by later publications of the same content.

Concurrent publishers may upload separate candidate objects. Only one row wins
the content identity constraint. A confirmed losing insert may delete its own
unpublished candidate. An uncertain insert error must retain its object: the
transaction may have committed before its reply was lost. Retrying identical
content can recover the committed row through the full content identity.

Legacy rows have no full digest. Their old object keys contain only 32 bits of a
hash, which cannot establish content identity. A candidate legacy duplicate is
read and its full content hash compared before reuse. Language edits obtain a
missing full digest from the stored bytes. Migration does not fabricate digests
or rewrite existing objects.

Metadata updates merge only supplied fields in one SQL update. An optional
expected revision is compared in that same statement. A database trigger
increments the revision for every update, including bridge and direct SQL
writers, so an intervening change invalidates a captured guard. Language changes
can conflict with an existing full content identity; a conflict leaves both rows
and objects intact.

Deletion removes the row before attempting object cleanup. A delayed cleanup
cannot remove a new publication because its object key differs. Successful
deletion means metadata is absent, not that physical cleanup is durable. Failed
cleanup and uncertain publication can leave orphan objects; they are logged,
but there is no durable orphan reconciliation job. A client must not infer a
physical erasure guarantee or durable operation replay from these methods.

All subtitle writers must use the immutable object implementation before relying
on these publication guarantees. The revision trigger invalidates guards for
older writers, but cannot make an older binary's object moves or shared-key
cleanup safe. This storage foundation alone does not enable API v2 mutations or
activate playback for existing accounts.

AI job cancellation attempts the guarded terminal job-state transition before
canceling a local worker context. A database error is returned even when local
work can be asked to stop. Already-terminal rows remain unchanged. This does
not fence publication: an in-flight worker can still store subtitle output
after cancellation or stale-job recovery. Immutable object ownership protects
other publications' bytes, but does not make job completion and subtitle
metadata publication atomic. AI mutation migration must resolve that boundary
before claiming durable cancellation of output.
