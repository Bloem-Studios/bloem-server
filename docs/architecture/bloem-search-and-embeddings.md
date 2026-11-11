# How search finds titles by words and by meaning

> **Provenance and status.** Bloem page, written on 2026-11-10 and updated on 2026-11-11
> against `main` at `e2806cad4`. Search and embeddings are Silo's. Bloem adds three
> things, carried until Silo has them: task labels for the `gemini-embedding-2` model, a
> choice to drop the most common words of a search first, and a names-first query in
> every hybrid search. The technical rules are in the [For developers](#for-developers) section.

When someone types into the search box, Bloem looks for titles in two ways at once: by
the words typed and by what they mean. This page walks through the two halves of a
search, what happens when no title has every word, how titles get the numbers the
meaning half compares, and what changing the AI model behind those numbers involves.

## Two halves of one search

![A made-up search, "man finds a message in a bottle", goes two ways: the words half matches its words in titles, people, genres, keywords and plots, dropping the most common words first when no title has every word; the meaning half turns the search into a vector labelled as a search and compares it with each title's vector. Bloem mixes the two lists, half from each by default. Along the bottom: the meaning half waits until 85% of each kind of title has a vector from the current model, and a search Meilisearch has not answered in time goes to the built-in engine](images/search-and-embeddings/two-halves.svg)

With **Meilisearch** as the search engine, a search runs like this:

1. **Words.** Meilisearch looks for the search's words in each title's name, its other
   names, its people, studios, networks, genres, keywords, plot and tagline. A match in
   the name counts most, a match in the tagline least. It forgives small typing mistakes:
   one wrong letter in a word of five letters or more, two in a word of nine or more.
2. **Meaning.** An **embedding** is a list of numbers, a **vector**, that an AI model
   makes from a piece of text. Texts that mean similar things get vectors that lie close
   together. Every title has one, made from its genres, plot, name, year, rating,
   tagline, people, keywords, studios and country. For a search of two or more words,
   Bloem asks the same model for the search's vector and looks for titles whose vectors
   lie close to it.
3. **Mixing.** Bloem merges the two lists. The **meaning-based share** sets how much
   each counts: 0 ranks by words only, 1 by meaning only, and the default is 0.5.
   Bloem's production uses 0.6.
4. **Names first.** Bloem also looks for the search's words in the titles' names alone,
   every word required, and ranks a title whose name has all of them above the mixed
   list. Someone typing a title, even with a typing mistake, still gets it first while
   meaning decides everything else.

The words half answers alone when the meaning half cannot help: a one-word search, a
search for episodes, and any kind of title that is still getting its vectors (see
[Changing the embedding model](#changing-the-embedding-model)). If Meilisearch has not
answered within the **query timeout**, the built-in PostgreSQL search answers instead.

## When no title has every word

![This example is made up. The search "man finds a message in a bottle on the beach" matches no title with all ten words. Dropping trailing words keeps "man finds a", which thousands of titles contain. Dropping the most common words first keeps "message bottle beach", the words that pick out the story. Along the bottom: two-word searches still need both words, and on 200 searches that describe a title, the right title was in the top 50 for 16.5% of searches with trailing words dropped and 36.5% with common words dropped first](images/search-and-embeddings/dropping-words.svg)

A search that describes a story, rather than naming a title, rarely matches every word
of any one title. The setting **When a search has several words** decides what the words
half drops until something matches:

- **Drop trailing words until something matches** is Silo's default. It drops words from
  the end, so a description ends up matched on its first, usually ordinary, words.
- **Drop the most common words until something matches** is Bloem's addition. It drops
  words like "the", "a" and "finds" first and keeps the rarer words that pick out a story.
  Bloem's production uses it.
- **Require every word** returns only titles with all of the search's words.

Whatever the setting, a search of exactly two words needs both words and looks only at
names and plots. A typing mistake such as "spnge bob" then cannot shrink to one loose
word.

## How titles get their vectors

A background job gives every title a vector. It runs every 15 minutes for titles that
have none yet, and in full on its schedule. The **embedding lock** remembers which model
made the vectors, and every later vector must come from the same model: vectors from
two models cannot be compared.

`gemini-embedding-2` expects a short **task label** at the start of its input instead of
a separate setting. Bloem sends a title's text as `title: none | text: …` and a search
as `task: search result | query: …`, the forms Google gives for search. Other models get
the plain text. The text Bloem stores beside each vector stays plain.

## Changing the embedding model

1. On **Integrations → Recommendations**, choose the provider and model.
2. Restart the server. It reads the model when it starts.
3. Press **Reset embeddings**. It deletes the lock, every vector, the taste clusters and
   each profile's saved recommendation rows, and clears every taste profile.
4. The background job embeds the whole catalog again with the new model.
5. Meaning-based search for each kind of title (movies, series, ebooks, audiobooks)
   comes back once 85% of that kind has a vector from the new model. Until then the
   words half answers alone, and recommendations fill in as vectors arrive.

On a big catalog this takes a while. On November 10, 2026, Bloem's production embedded
about 66,000 titles an hour with Gemini, about a day for 1.7 million titles. Changing the
task labels is the same as changing the model: every vector made with the old labels
has to be made again.

## A made-up example

This example is made up. Ana remembers a film about a man who finds a message in a
bottle on a beach, but not its name. She types "man finds a message in a bottle on the
beach".

1. No title has all ten words. The words half drops "the", "on", "in", "a", "man" and
   "finds" first and keeps "message", "bottle" and "beach". It finds titles whose plot
   mentions all three.
2. The meaning half finds the film whose plot reads "A widower discovers a love letter
   washed ashore and sets out to find its author", although it shares almost no words
   with her search.
3. Bloem mixes both lists. The film is near the top.

## Who can change what

| Who | What they can change |
| --- | --- |
| A platform administrator | On **Configure → Server → Search**: the search engine, **When a search has several words**, **Match by meaning as well as words**, the **Meaning-based share of results** and the **Query timeout**. On **Integrations → Recommendations**: the embedding provider and model, and **Reset embeddings**. |
| Anyone who searches | Nothing. Search works the same way for everyone, within the libraries they may see. |

## Words used in the code

| Plain words | In the code |
| --- | --- |
| Search engine | `catalog.search.provider`: `postgres` or `meilisearch` |
| Words half | Meilisearch keyword search, `buildMeilisearchKeywordSearchRequest` in `internal/catalog/search_meilisearch_provider.go` |
| Meaning half | Meilisearch hybrid search with a vector Bloem supplies; embedder `catalog.search.meilisearch.embedder` (`silo_recommendations`) |
| Match by meaning as well as words | `catalog.search.meilisearch.semantic_enabled` |
| Meaning-based share | `catalog.search.meilisearch.semantic_ratio` |
| When a search has several words | `catalog.search.meilisearch.matching_strategy`: `last`, `frequency` or `all` |
| Query timeout | `catalog.search.meilisearch.timeout_ms` |
| Vector | `media_item_embeddings.embedding`, stored as 3,072 numbers |
| Embedding model | `recommendations.embedding_model`, with `recommendations.embedding_provider_id` |
| Embedding lock | `server_settings` row `recommendations.embedding_lock` |
| Task labels | `withTaskLabel` in `internal/recommendations/embeddings/client.go`; `Client.EmbedQueries` |
| Reset embeddings | `POST /api/v2/admin/recommendations/embeddings/reset` |
| Ready for meaning | `semanticCoverageReadyRatio` (0.85) in `internal/catalog/semantic_coverage.go` |
| Compressed vectors | `catalog.search.meilisearch.binary_quantized` |

## For developers

Contracts: [catalog API](../catalog-api.md) (search reads and their fallback),
[admin recommendations API](../admin-recommendations-api.md) (status and the reset),
[admin settings API](../admin-settings-api.md) (connection checks). Silo's
[recommendations](recommendations.md) page covers the embedding jobs; Bloem's
[overlay on it](../bloem/overlays/architecture/recommendations.md) adds the labels.

### Search

- The keyword request carries `matchingStrategyForRequest`: the configured strategy,
  except that a two-term query uses `all` unless the strategy is `all` already. Terms are
  counted after `normalizeTitleForComparison`, which drops `and` and maps number words
  to digits. `attributesToSearchOnForRequest` limits a two-term query to
  `meilisearchTitleSearchAttributes`: `title`, `original_title`, `sort_title`,
  `title_variants` and `overview`.
- A search goes hybrid only when `semantic_enabled` is on, the query has at least two
  terms, it is not an episode-only search, `CoverageReady` reports every requested type
  at or above 0.85 of its eligible items carrying a vector from the locked model, and the
  query vector arrives. Query vectors are cached for 15 minutes (1,024 entries). A failed
  query embed puts the node in a cooldown during which searches run keyword-only. A search
  that mixes episodes and other types is a federated search whose episode query has no
  vector.
- Every hybrid query of two or more terms goes to Meilisearch as a federated
  multi-search with one more query (`bloem_search_title_boost.go`, hooked into
  `meilisearchClient.Search` and `FederatedSearch`): keyword only, `matchingStrategy`
  `all`, on `title`, `original_title`, `sort_title` and `title_variants`, with the same
  filter and 1.2 times the hybrid query's weight. Meilisearch returns each document once.
  In a search that mixes episodes and other types the title query joins the media
  query. If Meilisearch refuses the federated request with a 4xx, the original request
  runs unchanged; a timeout or 5xx is returned as it is.
- `timeout_ms` (default 800) bounds the search call. A search that fails or runs out of
  time is answered by the PostgreSQL provider. Indexing has its own two-minute limit
  (`meilisearchIndexingTimeout`), so the setting does not affect it.
- The matching strategy is a search parameter, not an index setting: changing it never
  rebuilds the index. The index settings are Silo's (`catalogSearchMeilisearchSettings`):
  the searchable attributes in the order listed above, Meilisearch's default ranking
  rules and typo tolerance, no synonyms or stop words.
- `frequency` is accepted by `NormalizeAdminSetting` (admin writes) and
  `CatalogSearchSettingsFromMap` (startup and the indexer) since `7acd2c7e0`. A server
  built before that rejects the stored value: its search falls back to PostgreSQL at
  startup and its indexer skips every sync. Before rolling the image back past
  `7acd2c7e0`, set the strategy to `last` or `all`.

### Embeddings and task labels

- `Client.Embed` sends catalog text as `title: none | text: <text>` and
  `Client.EmbedQueries` sends a search as `task: search result | query: <text>`, only when
  `recommendations.embedding_model` is exactly `gemini-embedding-2` or starts with
  `gemini-embedding-2-`. A vendor-prefixed name such as `google/gemini-embedding-2`,
  which the lock treats as the same model, is sent unlabelled.
- `Engine.EmbedSearchQuery` reaches `EmbedQueries` through `embedSearchText`, which
  checks for the method on the `embedder`, so the interface and its test fakes are
  unchanged. Item embedding (`embed_backfill.go`) and the admin connection check use
  `Embed`.
- Labels are added in the client, after `BuildEmbeddingText`. The stored
  `media_item_embeddings.canonical_text` and Pass 2's SQL rebuild of it stay unlabelled.
- `title: none` is used instead of the title on purpose: `BuildEmbeddingText` puts genres
  and plot ahead of the name so the name does not dominate. In the November 9 test,
  putting the real title in the slot scored no better.
- The same text embedded with and without labels gives vectors with a cosine of about
  0.85, so a change to the labels needs every vector made again. Pass 1 re-embeds rows
  whose `model` differs from the configured one. Setting the `model` of the rows to
  redo to another value queues them; production used `gemini-embedding-2-unlabeled` on
  November 10.
- Rewriting `recommendations.embedding_lock` to the new model in SQL, instead of the
  reset, also works: Pass 1 replaces rows of the old model over time and nothing is
  deleted. Until the pass finishes, similar-title, taste and cluster comparisons mix
  vectors from two models. Production switched to `gemini-embedding-2` this way on
  November 9 ([receipt](../operations/2026-11-10-search-and-embeddings-deployment.md)).

### Measurements

- **Task labels, November 9.** 6,432 catalog titles: 200 targets (80 movies, 40 series,
  40 ebooks, 40 audiobooks), 232 movies from 60 franchises and 6,000 random others. Gemini
  Flash wrote 200 searches that describe a target without naming it; production keeps no
  search history. Mean reciprocal rank of the target: 0.774 unlabelled, 0.923 labelled;
  first place 68.5% to 87.0%; top 10 92.5% to 99.0%. Labelling the titles alone gave most
  of the gain (0.890). The franchise test was too easy to separate the formats (99.4% and
  99.7%), and the two formats share half of each title's ten nearest titles.
- **Matching strategy, November 10.** The same 200 searches, keyword only, against the
  live index of 2.79 million documents, episodes excluded. Target in the top 50: `last`
  16.5%, `frequency` 36.5%, `all` 1.0%. Exact titles found first 73.5% under every
  strategy; titles with one typo 62.5% under `last` and 63.0% under `frequency`.
- **Meaning-based share and names first, November 11.** After the re-embed, the same
  searches against the live index, limited to the 163 targets still in the catalog (37
  books were in libraries deleted on November 10). Meilisearch's mix behaves like a
  switch: up to a share of 0.57 a strong keyword hit wins, and from 0.6 the list is the
  meaning-only list. Target in the top 10:

  | | Words only | 0.5 | 0.6 | 0.6 and names first |
  |---|---|---|---|---|
  | Descriptions | 28.8% | 62.0% | 63.8% | 63.8% |
  | Exact titles | 98.2% | 98.2% | 95.1% | 98.2% |
  | Titles with one typo | 93.3% | 93.3% | 78.0% | 93.3% |

  Of 22 everyday searches such as "funny movie about a dog" and "show about a chef
  running a restaurant", 20 returned word matches in unrelated ebooks up to 0.57 and the
  intended movies and shows at 0.6. A minimum ranking score on the title query (0.75 to
  0.9) cost typed titles and left two-word genre searches such as "zombie comedy" on books
  with those words in their names. The title query adds about 15 ms; embedding a new
  search takes about 200 ms, cached for 15 minutes.
- **Compressed vectors, November 11.** The index stores binary-quantized vectors. For
  the 163 descriptions, re-ranking Meilisearch's 500 nearest titles by the full 3,072
  numbers put the target first in 46.6% of searches instead of 40.5%, and in the top 10
  in 74.8% instead of 70.6%. The first 768 numbers, normalized, did nearly as well
  (45.4%, 73.6%). Full vectors would take about 15.6 GB in Meilisearch against about
  0.5 GB, so the index stays compressed for now.
- **Search time.** Meilisearch's request logs for November 3 to 9: 613 searches, median
  23 ms, 90th percentile 634 ms, slowest 1.53 s (Meilisearch's default search cutoff is
  1.5 s). Production's query timeout is 3,000 ms; the 800 ms default would have sent 56
  of them (9%) to PostgreSQL.

### Temporary seams

Listed in `contracts/seams.txt`, to drop when Silo has the change:
`internal/recommendations/embeddings/client.go` and
`internal/recommendations/search_query_vector.go` (labels, `9df39cadb`);
`internal/catalog/search_meilisearch_provider.go`, `internal/catalog/search_provider.go`
and `web/src/pages/admin-settings/SearchSettings.tsx` (`frequency`, `7acd2c7e0`);
`internal/catalog/search_meilisearch_client.go` (names first).
`internal/config/admin_settings.go` was already a seam.

Tests: `internal/recommendations/embeddings/bloem_task_labels_test.go`,
`internal/recommendations/bloem_search_query_labels_test.go`,
`internal/catalog/bloem_matching_strategy_test.go`,
`internal/catalog/bloem_search_title_boost_test.go`,
`internal/config/bloem_search_matching_test.go`,
`web/src/pages/admin-settings/bloemSearchMatchingStrategy.test.tsx`.

Related: [Silo's recommendations](recommendations.md),
[PostgreSQL search](postgres-search.md),
[AI provider authentication](ai-provider-authentication.md),
[Optional Meilisearch](../bloem/overlays/wiki/deployment/docker.md#optional-meilisearch).
