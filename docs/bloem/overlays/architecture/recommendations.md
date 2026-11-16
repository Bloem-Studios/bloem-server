# Bloem overlay: Recommendations

This overlay covers Silo's [`docs/architecture/recommendations.md`](../../../architecture/recommendations.md), which stays exactly as Silo wrote it.

Silo's page explains how every title gets an **embedding**, a list of numbers an AI model
makes from the title's text, and how recommendations and search compare them. Bloem
changes one thing: with the `gemini-embedding-2` model, it puts a short **task label** in
front of each text, because that model takes its task that way. Titles are sent as
catalog text and searches as searches. [How search finds titles by words and by
meaning](../../../architecture/bloem-search-and-embeddings.md) explains it in plain
words.

## What Bloem changes

- **Labelled input for `gemini-embedding-2`.** A title's text goes out as
  `title: none | text: …` and a search as `task: search result | query: …`. Other models
  get the text unchanged.
- **The stored text stays plain.** The text kept beside each vector is the same as in
  Silo, so Silo's check for changed text works as written.
- **New labels mean new vectors.** Vectors made with and without labels do not match,
  so changing the labels means embedding the catalog again, just like changing the
  model.

## Words used in the code

| On this page | In the code |
| --- | --- |
| Task label | `withTaskLabel` in `internal/recommendations/embeddings/client.go` |
| Embedding a title | `Client.Embed` |
| Embedding a search | `Client.EmbedQueries`, reached from `Engine.EmbedSearchQuery` through `embedSearchText` |

## For developers

### Section by section

**Location:** section "Embeddings", the second paragraph ("Embeddings use the AI
provider…").

**Bloem adds:** When `recommendations.embedding_model` is exactly `gemini-embedding-2`
or starts with `gemini-embedding-2-`, `Client.Embed` sends each text as
`title: none | text: <text>` and `Client.EmbedQueries` as
`task: search result | query: <text>`. `Engine.EmbedSearchQuery` calls `EmbedQueries`
when the embedder has it (`embedSearchText` in
`internal/recommendations/search_query_vector.go`), so the `embedder` interface and its
fakes are unchanged; the backfill and the admin connection check keep calling `Embed`.
A vendor-prefixed name such as `google/gemini-embedding-2`, which the lock treats as the
same model, is sent unlabelled.

**Location:** section "Embeddings", the bullet "The canonical text stored with a vector
is always the full text…".

**Bloem adds:** The label is added in the client after `BuildEmbeddingText`, so the
stored canonical text and Pass 2's SQL rebuild of it never carry it. Labelled and
unlabelled vectors of the same text have a cosine of about 0.85, so a label change
needs every vector redone. Pass 1 redoes rows whose `model` differs from the configured
model; setting the `model` of the rows to redo to another value (November 10:
`gemini-embedding-2-unlabeled`) queues them without a reset.

Temporary seams: `internal/recommendations/embeddings/client.go` and
`internal/recommendations/search_query_vector.go`, in `contracts/seams.txt`. Tests:
`internal/recommendations/embeddings/bloem_task_labels_test.go` and
`internal/recommendations/bloem_search_query_labels_test.go`.

Related: [How search finds titles by words and by
meaning](../../../architecture/bloem-search-and-embeddings.md).
