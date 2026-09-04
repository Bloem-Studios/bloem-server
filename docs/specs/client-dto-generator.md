# Client DTO Generator (R-19)

Extend the settings-bindings pipeline so the server repo also emits the full client DTO set —
the request/response/realtime wire shapes — as Kotlin `kotlinx.serialization` classes for the
bloem-android-v3 `:core` module. Status: DRAFT for owner approval. Evidence: paths cited inline
are current main (`a339fc0f`); client paths are bloem-android-v3 / bloem-android-v2 main.
Commands assume the repository root is the cwd.

Owner rulings this spec implements:

- **R-19** (client repo `docs/plan/03-rulings.md:27`): the server repo's generator is the single
  contract source; the captured-fixture repo stays conformance-test data only, never a
  generation input.
- **R-31 / R-32** (`03-rulings.md:40-41`): the v3 client may share no substantive line with v2
  except wire strings; field declarations inside `@Serializable` classes are COPY when their
  names, order, defaults and grouping match v2. The generator's output shape is therefore a
  provenance requirement, not a style choice (§2).

## 1. Why the server generates, and what exists today

Today `cmd/settingsgen` reads one manifest (`contracts/settings/v1/manifest.json` via
`internal/settingscontract.Load()`, `cmd/settingsgen/main.go:34`) and emits Go, TypeScript,
Kotlin and Swift bindings (`main.go:41-52`). The Kotlin emitter (`main.go:339-467`) writes a
single `SettingKeys.kt`; `make settings-bindings` (`Makefile:104-127`) writes it straight into
the sibling android checkout, and `make verify-settings-bindings` (`Makefile:132-140`, run in CI
at `.github/workflows/ci.yml:250`) regenerates the in-repo Go binding into a temp dir and diffs
it against the committed file. The v3 client already consumes that output at
`core/src/commonMain/kotlin/org/bloemserver/bloem/model/settings/SettingKeys.kt` and treats it
as exempt generated code (R-07, `03-rulings.md:15`).

The playback contract has a second precedent: `cmd/playbackfixtures` builds golden bodies from
the live Go types (`cmd/playbackfixtures/main.go:110-349`) into
`internal/playback/testdata/protocol_v3/` and `make verify-playback-fixtures`
(`Makefile:174-183`, CI `ci.yml:258`) fails when they drift. The v3 client vendors those JSON
files byte-identically and decodes them through its DTOs
(`core/src/androidUnitTest/kotlin/org/bloemserver/bloem/model/playback/PlaybackProtocolV3ConformanceTest.kt:22-27`).

What is missing is the DTO layer itself: v2 hand-transcribed 7,791 model lines / 324
`@Serializable` classes (client `docs/plan/tasks/WP-05-core-dto-codegen.md:6-7`), and the v3
identity audit found the `:core` model files are byte-for-byte v2
(`docs/plan/08-identity-audit.md:9`; the per-file table at `:63-146` is the WP-03 remediation
group this generator replaces). WP-05 is blocked on this spec
(`docs/plan/04-status.md:29,45`).

## 2. Provenance requirement: the output shape must differ from v2

App-store approval requires the v3 client to contain no code derived from v2 (R-31 rationale).
A generated file carries a verifiable DO NOT EDIT header and is R-07-exempt, but the owner's
R-32 ruling makes the *shape* of a field block a derivation question, so the generator is
designed such that no non-wire line it emits can coincide with a v2 line. The guarantees, each
enforced by a generator test (§8):

1. **Kotlin property names come from the Go field name** through the generator's own rule
   (§4.3), never from a wire-string-to-camelCase rule (which is what v2 used and would
   reproduce v2's names).
2. **Declaration order is the Go struct order.** v2 grouped and reordered by hand.
3. **`@SerialName` on every property, on its own line**, including properties whose wire name
   equals the property name. v2 omitted it when the names matched and put it inline.
4. **Explicit `public` on every declaration** (class, constructor property, companion,
   constant). v2's model files contain no `public` modifier at all (grep over
   bloem-android-v2 `shared/src/commonMain/kotlin/org/bloemserver/bloem/model/` finds zero
   `public val` / `public data class`), so every declaration line differs.
5. **Defaults and nullability are computed from Go tags by rule** (§4.4), not chosen.
6. **Class names are the Go type names verbatim** (`TimelineV3`, not v2's
   `PlaybackTimelineV3`), qualified by a Kotlin package derived from the Go package.
7. **One file per Go package with a generated header** carrying the server git SHA and the
   generator version (§5.2).
8. **No hand-written functions in generated files.** Generated output contains no `fun`
   declarations; helpers, presentation logic and custom serializers live in hand-written files
   that import the generated types (§9.2).

### 2.1 Before / after

Go source, `internal/playback/protocol_v3.go:662-671`:

```go
type TimelineV3 struct {
	SourceStartSeconds     float64  `json:"source_start_seconds"`
	StreamOriginSeconds    float64  `json:"stream_origin_seconds"`
	PlayerStartSeconds     float64  `json:"player_start_seconds"`
	TimelineOffsetSeconds  float64  `json:"timeline_offset_seconds"`
	SeekWindowStartSeconds *float64 `json:"seek_window_start_seconds,omitempty"`
	SeekWindowEndSeconds   *float64 `json:"seek_window_end_seconds,omitempty"`
	CanSeekAnywhere        bool     `json:"can_seek_anywhere"`
	SeekRestoration        string   `json:"seek_restoration"`
}
```

**Before** — v2 hand-written DTO, bloem-android-v2
`shared/src/commonMain/kotlin/org/bloemserver/bloem/model/playback/PlaybackProtocolV3.kt:269-278`
(the v3 checkout currently carries the identical block at
`core/src/commonMain/kotlin/org/bloemserver/bloem/model/playback/PlaybackProtocolV3.kt:262-271`,
which is the audit finding):

```kotlin
@Serializable
data class PlaybackTimelineV3(
    @SerialName("source_start_seconds") val sourceStartSeconds: Double = 0.0,
    @SerialName("stream_origin_seconds") val streamOriginSeconds: Double = 0.0,
    @SerialName("player_start_seconds") val playerStartSeconds: Double = 0.0,
    @SerialName("timeline_offset_seconds") val timelineOffsetSeconds: Double = 0.0,
    @SerialName("seek_window_start_seconds") val seekWindowStartSeconds: Double? = null,
    @SerialName("seek_window_end_seconds") val seekWindowEndSeconds: Double? = null,
    @SerialName("can_seek_anywhere") val canSeekAnywhere: Boolean = true,
    @SerialName("seek_restoration") val seekRestoration: String = "player_position",
)
```

**After** — what the generator emits for the same struct (response-reachable type, §4.4), in
`contract/playback/Playback.kt`:

```kotlin
// Code generated by cmd/clientdtogen from bloem-server internal/playback. DO NOT EDIT.
// Server revision: a339fc0f2c1e… (generator v1, registry contracts/client/v1/registry.json)
// Regenerate in the server repo with: make client-dtos

package org.bloemserver.bloem.contract.playback

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Wire type `playback.TimelineV3`. Direction: response. Dialect: upstream-compat. */
@Serializable
public data class TimelineV3(
    @SerialName("source_start_seconds")
    public val sourceStartSeconds: Double = 0.0,
    @SerialName("stream_origin_seconds")
    public val streamOriginSeconds: Double = 0.0,
    @SerialName("player_start_seconds")
    public val playerStartSeconds: Double = 0.0,
    @SerialName("timeline_offset_seconds")
    public val timelineOffsetSeconds: Double = 0.0,
    @SerialName("seek_window_start_seconds")
    public val seekWindowStartSeconds: Double? = null,
    @SerialName("seek_window_end_seconds")
    public val seekWindowEndSeconds: Double? = null,
    @SerialName("can_seek_anywhere")
    public val canSeekAnywhere: Boolean = false,
    @SerialName("seek_restoration")
    public val seekRestoration: String = "",
)
```

Line-by-line: the only strings shared with v2 are the eight `@SerialName("…")` wire names,
which R-31(a) exempts. The class name, every declaration line, the defaults
(`canSeekAnywhere = false`, `seekRestoration = ""` are Go zero values, where v2 chose
`true` / `"player_position"` — v2's defaults encoded a client policy inside a DTO, which the
generator refuses to do; that policy moves to a hand-written v3 helper) and the header differ.
The wire bytes are identical in both directions.

## 3. Inputs: selecting the Go wire types

### 3.1 The candidates

| Approach | How | Trade-offs |
|---|---|---|
| **Explicit registry** (recommended) | `contracts/client/v1/registry.json` lists root types per Go package with direction, dialect and gate metadata; the generator loads the packages with `golang.org/x/tools/go/packages` (already a dependency, `go.mod:43`) and walks the type graph transitively from the roots. | + Reviewable, diffable list of what the contract is; + carries metadata (dialect, gate, custom serializer) without touching Go files; + reaches unexported handler types (`capabilityResponse`, `internal/api/handlers/notifications.go:379`) because go/types sees them; − roots must be added by hand when a new endpoint lands (the drift/coverage test in §8 flags a registered package whose exported struct set changed). |
| Marker comment (`//bloem:client-dto` above the type) | go/ast scan for the marker. | + Self-documenting at the definition; − the input is scattered across ~100 files and invisible in review; − cannot express dialect or direction per root without a second syntax; − requires editing upstream-identical files, which the Silo-path ruling forbids (favor Silo paths, keep divergences as runtime branches). |
| Route walk (derive types from the router's handler signatures) | Inspect `mountV2Routes` / v1 router, follow `writeJSON(w, …, value)` arguments. | Not feasible: 448 anonymous `struct{…}` literals and `map[string]any` payloads in `internal/api/handlers/` (e.g. `notifications.go:307`) have no nameable type; static analysis of `writeJSON` call sites would need a full dataflow pass and would still miss realtime frames, which are not routes. |

**Recommendation: explicit registry.** It is the same shape as the settings manifest — a
committed contract artifact the generator reads — which keeps the R-19 "single contract source"
property literal. Transitive closure means the registry lists roots only: registering
`playback.DecisionResponseV3` (`protocol_v3.go:863`) pulls in `PlanV3`, `StreamV3`,
`TimelineV3`, … automatically, and a type reachable from two roots is generated once.

### 3.2 Registry format

```json
{
  "schema": 1,
  "packages": [
    {
      "path": "internal/playback",
      "dialect": "upstream-compat",
      "roots": [
        {"type": "StartRequestV3",      "direction": "request"},
        {"type": "ReplanRequestV3",     "direction": "request"},
        {"type": "RouteEventV3",        "direction": "request"},
        {"type": "DecisionResponseV3",  "direction": "response"},
        {"type": "CapabilityResponseV3","direction": "response"},
        {"type": "ErrorResponseV3",     "direction": "response"},   // internal/playback/conformance_v3.go:6
        {"type": "EventEnvelope",       "direction": "response", "gate": "realtime"},
        {"type": "CommandEnvelope",     "direction": "response", "gate": "realtime"},
        {"type": "HelloEnvelope",       "direction": "request",  "gate": "realtime"},
        {"type": "AckEnvelope",         "direction": "request",  "gate": "realtime"},
        {"type": "ResultEnvelope",      "direction": "request",  "gate": "realtime"},
        {"type": "ChapterThumbnailReadyPayload", "direction": "response", "gate": "realtime"}
      ]
    },
    {
      "path": "internal/api/handlers",
      "dialect": "upstream-compat",
      "roots": [
        {"type": "capabilityResponse", "direction": "response",
         "bloem_fields": ["announcements", "supported_types", "dismiss", "ambience", "promotions", "remote_control"]},
        {"type": "remoteCapabilityRequest",  "direction": "request",  "dialect": "bloem", "gate": "notifications.remote_control"},
        {"type": "remoteCapabilityResponse", "direction": "response", "dialect": "bloem", "gate": "notifications.remote_control"}
      ]
    },
    {
      "path": "internal/promotions",
      "dialect": "bloem",
      "gate": "notifications.promotions",
      "roots": [{"type": "Card", "direction": "response"}]
    },
    {
      "path": "internal/ambience",
      "dialect": "bloem",
      "gate": "notifications.ambience",
      "roots": [{"type": "Wire", "direction": "response"}]
    }
  ],
  "serializers": {
    "internal/api/handlers.itemListResponse.frame_rate": "org.bloemserver.bloem.model.wire.FrameRateWire"
  }
}
```

Field meanings:

- `path` — Go package, repository-relative. One output file per entry (§5.1).
- `dialect` — `upstream-compat` (shape exists on Silo too) or `bloem` (Bloem-only). Package-level
  default, overridable per root. `bloem_fields` marks Bloem-only fields inside an
  upstream-compat type (the S-1/S-2/S-3/S-5a additions to `capabilityResponse`,
  `notifications.go:389-402`). Dialect never changes the generated Kotlin type; it is emitted
  as KDoc and into the dialect table (§5.3) so conformance tests know which fixture set must
  decode a type. Field-level marking lives in the registry, not in struct tags, because the
  owning Go files are upstream-identical and must not be edited for client concerns.
- `direction` — `request`, `response`, or `both`; drives the default/nullability rule (§4.4).
  Inherited transitively: a type reached from a response root is response-reachable.
- `gate` — the capability that must be advertised before a client decodes the type (§7.3).
  Emitted as KDoc and into the gate table.
- `serializers` — the only escape hatch (§9.2): a field whose Go side has custom
  `MarshalJSON`/`UnmarshalJSON` or a polymorphic wire shape gets `@Serializable(with = …)`
  pointing at a **client-side** class the registry names. The generator never emits the
  serializer body.

Type-level facts the generator refuses (fails with the type name) rather than guesses:
generic types (`optionalField[T]`, `internal/api/handlers/admin.go:338`), fields with the
`,string` option, custom marshalers without a `serializers` entry, untagged exported fields,
and any registered root not found in its package. Refusing is what keeps the registry honest:
a request-side helper type that exists to tolerate sloppy input is not a contract shape.

### 3.3 Realtime frames

The `/playback/sessions/{id}/control/ws` frames are plain structs with `json` tags
(`internal/playback/realtime.go:116-121` `EventEnvelope`, `:394-403` `CommandEnvelope`,
`:514-519` `HelloEnvelope`, `:549-554` `AckEnvelope`, `:571-577` `ResultEnvelope`) whose
`payload` is `json.RawMessage` selected by `name`. They register like any other root; the
payload structs (`ChapterThumbnailReadyPayload` `:124-130`, `MarkersUpdatedPayload`
`:137-144`, `SubtitleTranslation*Payload` `:175-221`, `PlanInvalidatedPayload` `:423-426`,
remote command payloads `internal/remote/commands.go:131-181`) register as response roots so
the client's hand-written dispatcher decodes `payload` into them by `name`. The
`CommandName` / `RealtimeEventName` string sets (`realtime.go:21-30`, `:44-63`) become
generated wire-constant holders (§4.2), which is what lets the client's `hello` advertise
exactly the server's vocabulary without a hand-kept list.

## 4. Go → client mapping

Kotlin is the reference rendering: §4.1–§4.4 are written in it because it landed first. §4.5
records the places Swift is forced to answer differently; a row not listed there is the same
rule in both.

### 4.1 Types

| Go | Kotlin | Notes / evidence |
|---|---|---|
| `string` | `String` | |
| named `string` type with `const` block (`StreamProtocolV3`, `protocol_v3.go:197`; `CommandName`, `realtime.go:44`; `remote.Scope`, `commands.go:18`) | `@JvmInline public value class StreamProtocolV3(public val wire: String)` with a `public companion object` holding one `public val` per Go constant | §4.2. Unknown values decode without failing, which an `enum class` cannot guarantee for response types. |
| `bool` | `Boolean` | |
| `int`, `int32`, `uint16`, `uint32` | `Int` | Go `int` is 64-bit on the server but the contract uses it for counts and small ids (`FileID int`, `realtime.go:126`). |
| `int64`, `uint64` | `Long` | Row ids (`ID int64`, `internal/api/handlers/api_keys.go:48`), `JobID int64` (`realtime.go:178`), `PositionMS int64` (`commands.go:132`). |
| `float32`, `float64` | `Double` | |
| `time.Time` | `String` | encoding/json emits RFC 3339 with nanoseconds; the server also pre-formats some dates by hand (`items.go:1323` `"2006-01-02"`), so a typed instant would be wrong for those anyway. Parsing is a hand-written client concern (open question 3). |
| `*time.Time` | `String?` | `WithdrawnAt *time.Time` (`internal/notifications/announcement_types.go:50`). |
| `uuid.UUID` / `*uuid.UUID` | `String` / `String?` | `OrganizationID *uuid.UUID` (`internal/promotions/promotion.go:93`). |
| `json.RawMessage` | `JsonElement?` (`kotlinx.serialization.json.JsonElement`) | Envelope payloads (`realtime.go:120`, `:402`), `remote.Command.Payload` (`internal/remote/store.go:32`). Always nullable: Go emits `null` for a nil RawMessage without omitempty. |
| `any`, `map[string]any` | `JsonElement?`, `Map<String, JsonElement>` | |
| `[]T` | `List<T>` | Go marshals a nil slice as `null`, not `[]`: the default is `emptyList()` and decoding relies on `coerceInputValues = true` in the client's shared `BloemJson` (`core/src/commonMain/kotlin/org/bloemserver/bloem/network/BloemHttpClient.kt:19-25`). The header states this requirement (§5.2). |
| `*[]T` | `List<T>?` | Present-vs-absent is meaningful: `Ambience *[]ambience.Wire` (`notifications.go:396`) — key absent = dormant, `[]` = nothing active. |
| `[]byte` | `String` | encoding/json base64. |
| `map[string]T` | `Map<String, T>` | `Headers map[string]string` (`protocol_v3.go:657`). |
| `*T` (struct or scalar) | `T?` | `PlaybackPlan *PlanV3` (`protocol_v3.go:869`), `Index *int` (`:524`). |
| struct field of struct type `T` | `T` | Nested class, same or imported package. |
| **embedded** struct (`playbackSessionRow` inside `remoteSessionRow`, `internal/api/handlers/remote_control.go:277`) | fields inlined at the embed point, in the embedded struct's order | Mirrors encoding/json promotion. The embedded type is still generated on its own if it is a root or referenced elsewhere. Embedded pointers and embedded types with their own `json` tag are refused (none exist today). |
| type alias (`promotions.Targeting = notifications.AnnouncementTargeting`, `promotion.go:46`) | resolved to the target; one class in the target's package file, imported by the user | |
| `json:"-"` | omitted | |
| unexported field | omitted | encoding/json skips it; the generator does the same and emits nothing. |
| no `json` tag on an exported field | **error** | encoding/json would use the Go name; the contract must be explicit (open question 5). |
| `,omitempty` / `,omitzero` (`Failure FailureV3 \`json:"failure,omitzero"\``, `protocol_v3.go:581`) | see §4.4 | Both treated as "may be absent on the wire". |
| `,string` option | **error** | None in the registered set; refusing beats silent misdecoding. |

### 4.2 String enumerations

Go's named string types carry their vocabulary as `const` blocks, not as a closed type, and
Go code accepts unknown values through them (`DecisionOutcomeV3`, `protocol_v3.go:155`;
the realtime name sets are validated only by explicit maps, `realtime.go:33-41`). A Kotlin
`enum class` closes the set: a server that adds an outcome breaks every older client at decode
time, exactly the failure mode the v1 rules forbid (`AGENTS.md:180-186`, additive-only after
lock). The generator therefore emits a value class:

```kotlin
/** Wire type `playback.StreamProtocolV3`. */
@Serializable
@JvmInline
public value class StreamProtocolV3(public val wire: String) {
    public companion object {
        @SerialName("hls")
        public val HLS: StreamProtocolV3 = StreamProtocolV3("hls")
        @SerialName("progressive")
        public val PROGRESSIVE: StreamProtocolV3 = StreamProtocolV3("progressive")
        public val KNOWN: List<StreamProtocolV3> = listOf(HLS, PROGRESSIVE)
    }
}
```

The constant identifier is the Go constant name with the type-name prefix removed and
converted to SCREAMING_CASE (`StreamProtocolHLSV3` → `HLS`), the same rule `settingsgen`
already applies to keys (`main.go:104-108`). Constants keep Go source order. `KNOWN` lists the
vocabulary the server compiled with; a client that needs "is this one I understand" compares
against it, and a client that needs to advertise the full server vocabulary (the realtime
`hello`, `realtime.go:528-530`) reads it. This is a generated table, not a helper function.

### 4.3 Property-naming rule

Input is the **Go field name**, never the wire string. Steps:

1. Split the Go identifier into words on lower→upper boundaries and on digit boundaries,
   treating a run of capitals followed by a capital+lowercase as an initialism ending before the
   last capital (`HDRDetails` → `HDR`, `Details`; `BLCompatibilityIDs` → `BL`,
   `Compatibility`, `IDs`; `HDR10MaxWidth` → `HDR`, `10`, `Max`, `Width`; `DVProfile` → `DV`,
   `Profile`; `MIMEType` → `MIME`, `Type`).
2. Lowercase every word; capitalise the first letter of every word but the first; join.
3. If the result is a Kotlin hard keyword (`in`, `is`, `as`, `fun`, `object`, …) append
   `Value`.

Examples: `HDRDetails` → `hdrDetails`; `BLCompatibilityIDs` → `blCompatibilityIds`;
`HDR10MaxWidth` → `hdr10MaxWidth`; `DVProfile` → `dvProfile` (wire `dolby_vision_profile`,
`protocol_v3.go:710` — the v2 name `dolbyVisionProfile` was wire-derived and is *not* what the
generator produces); `MIMEType` → `mimeType`; `ID` → `id`; `URL` → `url`;
`InApp` → `inApp`; `Type` → `type` (allowed as an identifier).

The rule is deterministic and total; the generator test pins it with a table (§8). Where Go
and wire names agree on the words, the camelCase result can coincide with v2's *identifier* —
`sourceStartSeconds` above — and that is acceptable under R-31 because the full declaration
line never coincides (guarantees 3 and 4 in §2). Open question 1 offers the stricter
alternative (Go-identical `PascalCase` properties) should the owner want identifier-level
divergence as well.

### 4.4 Nullability and defaults

Direction comes from the registry root and propagates through the graph. A type reachable from
any `response` or `both` root is **response-reachable**.

| Go field | Response-reachable type | Request-only type |
|---|---|---|
| pointer, `*T` | `T? = null` | `T? = null` |
| `omitempty`/`omitzero`, non-pointer scalar | zero-value default (`0`, `0.0`, `false`, `""`) | zero-value default (Go omits the zero, so sending it equals omitting it) |
| `omitempty` slice / map | `emptyList()` / `emptyMap()` | same |
| non-`omitempty` scalar | zero-value default | **required** (no default) |
| non-`omitempty` slice / map | `emptyList()` / `emptyMap()` (see `coerceInputValues` note) | required |
| non-`omitempty` struct `T` | `T()` — valid because every response-reachable class has an all-defaults constructor by induction | required |
| `json.RawMessage`, `any` | `JsonElement? = null` | `JsonElement? = null` |

Why response types default everything: the client must decode any server it meets —
upstream-compat servers omit every Bloem-dialect key, and post-lock servers add keys. The
WP-05 acceptance line "any field that exists only in the Bloem dialect must be optional with a
default" (`WP-05-core-dto-codegen.md:41-42`) is met for *all* fields by construction, so a
forgotten `bloem_fields` entry cannot break decoding. Why request types keep required fields:
a compile error is the right signal when the client fails to supply something the server
demands, and `encodeDefaults = true` (`BloemHttpClient.kt:22`) means defaults are written, so
request bytes stay byte-compatible with what v2 sent.

`@SerialName` values are the `json` tag name copied byte-for-byte; the generator never
transforms them.

### 4.5 Where Swift diverges (chunk C7)

The Swift emitter takes §4.1–§4.4 decision for decision. These are the places the language
forces a different answer; everything not listed here is the same rule, spelled in Swift.

| Row | Kotlin | Swift | Why |
|---|---|---|---|
| package | `package org.bloemserver.bloem.contract.playback` | `public enum Playback { … }` in `playback/Playback.swift` | Swift has no per-directory namespace and the registry already holds two types called `Status`; a caseless enum is the language's namespace. |
| annotation | `@SerialName("source_start_seconds")` | `case sourceStartSeconds = "source_start_seconds"` in a `public enum CodingKeys` | Swift carries wire names in `CodingKeys`, which is also the one construct the Apple identity gate exempts. |
| defaults | constructor default + `coerceInputValues` | a generated `public init(from:)` reading `decodeIfPresent(…) ?? default` | Swift's synthesized `Decodable` **ignores** property defaults and throws on a missing key. Without an explicit initializer, an upstream-compat server omitting a Bloem key would fail to decode. `decodeIfPresent` returns nil for an absent key *and* for an explicit `null`, which is exactly `coerceInputValues`. |
| encoding | `explicitNulls = false`, `encodeDefaults = true` | a generated `public func encode(to:)` using `encodeIfPresent` for optionals and `encode` otherwise | Same bytes on the wire; written out because a boxed field (below) defeats synthesis. Property references are `self.`-qualified: a wire field may be called `container`. |
| `int`, `uint32` | `Int` (32-bit) | `Int` (64-bit on every Apple platform) | Strictly wider; `uint32` values above 2^31 decode on Apple and would fail on Android. `int64`/`uint64` are `Int64` in both. |
| enumerations | `@JvmInline value class X(val wire: String)` | `public struct X: RawRepresentable, Codable` with `wire`/`rawValue` | The standard library's `RawRepresentable` conformance decodes and encodes it as a bare string, so an unknown server value survives, which is the whole point of §4.2. Constant identifiers stay SCREAMING_CASE, against Swift convention, so both clients name one vocabulary identically. |
| `json.RawMessage`, `any` | `kotlinx.serialization.json.JsonElement` | the client-owned `BloemJSONValue` | Swift's standard library has no JSON value type. The client owns it like it owns the registry serializers (§9.2); it must be `Codable`, `Hashable`, `Sendable`. |
| self-referential field (`Next *Child`) | `Child? = null` | `public var next: Child? { _next.value }` over `private let _next: Indirect<Child?>` | A Swift struct stores its fields inline, so `struct Child { let next: Child? }` is rejected outright. The generator boxes exactly the fields on a containment cycle; a cycle with no pointer in it is refused. |
| registry `serializers` | the `kotlin` target, fully qualified, in `@Serializable(with = …)` | the `swift` target, else the last component of the `kotlin` target | No entry in `contracts/client/v1/registry.json` names a `swift` target yet, and adding one would move `CONTRACT_DIGEST` and force an Android re-vendor for a change that alters no wire shape. The fallback is deterministic and is the name a human would write; an entry naming neither target is refused. |
| type named `Protocol`, `Self` or `Any` | emitted verbatim | declared with backticks; **refused** when referenced across namespaces | `Namespace.Protocol` is metatype syntax and backticks do not rescue the qualified form. No such type exists in the registry today; renaming the Go type is the fix. |
| `time.Time`, `uuid.UUID` | `String` | `String` | Not a divergence — recorded because the tempting Swift answers (`Date`, `UUID`) are wrong for the same reason §4.1 gives: the server hand-formats some dates, and a strict `UUID` would refuse a string the server may legitimately send. |

## 5. Outputs

### 5.1 Layout in the client

Generated sources are committed in the client repo under a dedicated source directory of
`:core` so a reader can tell generated from authored at a glance:

```
core/src/commonMain/kotlin-generated/org/bloemserver/bloem/contract/
    GeneratedContract.kt          # revision, digest, dialect + gate tables
    playback/Playback.kt          # internal/playback
    handlers/Handlers.kt          # internal/api/handlers
    notifications/Notifications.kt
    promotions/Promotions.kt
    ambience/Ambience.kt
    remote/Remote.kt
```

One file per Go package (ruled); Kotlin package = `org.bloemserver.bloem.contract.` +
last Go path element; file name = capitalised last path element. `kotlin-generated` is added to
the `commonMain` source set in `core/build.gradle.kts` and excluded from ktlint/detekt, so the
explicit-`public` style and long files do not need lint suppressions.

The Swift tree mirrors it directory for directory — `swift/playback/Playback.swift`,
`swift/GeneratedContract.swift` — with the Kotlin package replaced by a caseless-enum namespace
(§4.5), so `contracts/client/v1/kotlin/` and `contracts/client/v1/swift/` can be read side by
side. Existing hand-written
files under `model/…` shrink to the survivors in §9.2 and import `contract.…`.

### 5.2 File header and determinism

```kotlin
// Code generated by cmd/clientdtogen from bloem-server internal/playback. DO NOT EDIT.
// Server revision: <full git SHA> (generator v1, registry contracts/client/v1/registry.json)
// Regenerate in the server repo with: make client-dtos
//
// Decoding requires the shared Json configuration: ignoreUnknownKeys, coerceInputValues,
// explicitNulls = false, encodeDefaults (see network/BloemHttpClient.kt).
```

The revision comes from `-server-revision`, populated by the Makefile the way `BUILD_REVISION`
already is (`Makefile:24`, `git rev-parse HEAD`). It is the one non-deterministic line, so the
drift check compares files with the `// Server revision:` line stripped (§6.3);
`GeneratedContract.kt` additionally exposes it as `public const val SERVER_REVISION`.

Ordering: packages in registry order; within a file, types sorted by Go type name
(stable across moving a type between Go files, the same reasoning as
`sortedDefinitions`, `main.go:79-89`); fields in Go declaration order; enum constants in Go
source order; imports sorted. Two runs on the same tree produce identical bytes.

### 5.3 `GeneratedContract.kt`

```kotlin
public object GeneratedContract {
    public const val SERVER_REVISION: String = "a339fc0f…"
    public const val GENERATOR_VERSION: Int = 1
    /** Digest of the normalised type graph; changes only when a shape changes (§7.2). */
    public const val CONTRACT_DIGEST: String = "sha256:…"
    /** Fully qualified class name → dialect, from the registry. */
    public val DIALECT: Map<String, String> = mapOf(…)
    /** Fully qualified class name → capability gate, for gated types only. */
    public val GATE: Map<String, String> = mapOf(…)
}
```

Tables, not functions: conformance tests and the capability layer read them.

## 6. Invocation, consumption, drift

### 6.1 Server side

```
make client-dtos           # regenerate into contracts/client/v1/kotlin/ (committed)
make verify-client-dtos    # regenerate into a temp dir, diff against the committed copy
```

`cmd/clientdtogen -registry contracts/client/v1/registry.json -lang kotlin
-out-dir contracts/client/v1/kotlin -server-revision $(BUILD_REVISION)`. Like the settings
bindings, the server commits its own copy of the output (the Go binding
`internal/settingskeys/keys.go` and TS binding are in-repo, `Makefile:106-108`) so the drift
check needs no client checkout. `make client-dtos` additionally copies into
`$(BLOEM_ANDROID_DIR)/core/src/commonMain/kotlin-generated/` when that checkout exists, the
same convenience `settings-bindings` offers (`Makefile:111-118`; note the variable there is
still `SILO_ANDROID_DIR` and the path is v2's `shared/…` layout — chunk C3 renames it and
points at v3).

### 6.2 Client side

The client vendors the files from the server commit it pins. `core/contract-pin.txt` holds the
server SHA; a small Gradle task `verifyContractPin` asserts every generated file's
`// Server revision:` line equals the pin. Conformance tests decode the vendored server fixtures
(`internal/playback/testdata/protocol_v3/*.json`, already vendored under
`core/src/androidUnitTest/resources/playback/v3/`) through the generated classes. Updating the
contract is: bump the pin, copy the generated directory and fixtures from that server commit,
run the tests.

### 6.3 Where the drift check runs — and why

**Recommendation: server CI regenerates and diffs (authoritative); client CI checks the pin.**

- Server CI (`ci.yml`, next to `verify-settings-bindings` at `:250`) runs
  `make verify-client-dtos`. A PR that changes a registered struct without regenerating fails
  here, in the repo where the change is made, by the person making it. This is the property
  the settings and playback-fixture gates already provide (`ci.yml:247-258` comments).
- Client CI cannot regenerate: it would need a Go toolchain, a checkout of the private server
  repo at the pinned SHA, and credentials for it, in an Android build. It can and does verify
  that what it compiled against is what it claims to have vendored (the pin check) and that the
  fixtures decode. Regenerating at the pinned SHA in client CI would only re-prove the server's
  own gate.
- Server-CI-diff-against-the-client-checkout (the third option) couples server CI to the
  client's branch state and turns every client-side lag into a red server build; rejected.

## 7. Versioning

### 7.1 Additive changes

A new field, a new enum constant, a new root: regenerate, commit both copies. Every generated
response property has a default, so the client compiles and decodes unchanged. Pre-lock, this
is the common case.

### 7.2 Breaking changes

Rename / removal / type change of a field. Pre-lock these are allowed and must be coordinated
with both clients (`AGENTS.md:172-178`). The signal is mechanical: the regenerated diff removes
or retypes a public property, and `CONTRACT_DIGEST` changes. The generator additionally writes
`contracts/client/v1/digest.txt`; a server-side test (§8) fails when the digest changes without
a matching line in `docs/architecture/v1-scope.md`'s pre-lock removals table, which is where
`AGENTS.md:176-178` already says removals are recorded. Post-lock the same test flips to
"digest may not change except by adding".

### 7.3 Capability-gated types

S-1 alerts, S-2 promotions, S-3 ambience and S-5 remote control are Bloem-dialect features
discovered through capability payloads, never by version sniffing
(`docs/specs/client-engagement.md:8-16`; `capabilityResponse`, `notifications.go:379-403`).
Gated types are generated unconditionally — they cost nothing when dormant — and carry their
gate in KDoc and in `GeneratedContract.GATE`. The contract for a client is: do not call the
endpoint or decode the payload unless the named capability key is present. Absence is modelled
exactly as the Go side does: `Promotions *capabilityPromotions \`json:"promotions,omitempty"\``
→ `promotions: CapabilityPromotions? = null` (null = dormant), `Ambience *[]ambience.Wire` →
`List<Wire>? = null` (null = dormant, empty = nothing active). Nothing in the generator is
dialect-conditional, which keeps the client's own "capability present / absent" tests the only
place that behaviour is decided.

## 8. Generator test strategy

- **Golden files.** A synthetic Go package `cmd/clientdtogen/internal/fixture` declares one
  struct per mapping row in §4.1/§4.4 (pointer, omitempty, omitzero, embedded, alias,
  `RawMessage`, `*[]T`, `time.Time`, `uuid.UUID`, named string type with constants, keyword
  field name, every initialism case in §4.3) plus a registry for it. Golden Kotlin under
  `cmd/clientdtogen/testdata/kotlin/` is compared byte-for-byte; `-update` rewrites it. This is
  the test that pins the output shape.
- **Provenance assertions** over the golden output: every `val` line starts with `public val`;
  every property has a preceding `@SerialName` line; no `fun ` token anywhere; every class
  name equals a Go type name in the fixture.
- **Naming-rule table test** for §4.3, including the keyword case.
- **Refusal tests**: generics, `,string`, untagged field, custom marshaler without a registry
  serializer, unknown root — each must fail with the offending type in the message.
- **Round-trip against captured fixtures.** For every response root, a Go test unmarshals each
  server golden fixture (`internal/playback/testdata/protocol_v3/*.json` today; C4 adds S-1/S-2/S-3
  bodies captured from the handler tests) into the Go type, re-marshals, and checks that every
  key in the fixture maps to a generated `@SerialName` (parsing the generated Kotlin's
  annotation lines). This proves coverage from the server side without a Kotlin compiler in Go
  CI. The Kotlin-side decode of the same fixtures is the client's existing conformance harness.
- **Coverage drift.** For each registered package, the test records the set of exported struct
  types carrying `json` tags; an unregistered, unreached type is reported as a warning with the
  package and name, so new wire shapes get registered rather than forgotten.
- **Determinism.** Generate twice, compare bytes.

## 9. Client migration

### 9.1 Order of the WP-03 remediation group

The 44 model files (`08-identity-audit.md:63-146`) fall into three groups. Migrate by
verification strength, one domain per commit, deleting the replaced hand-written file in the
same commit (WP-05 steps, `WP-05-core-dto-codegen.md:31-34`):

1. **Playback protocol first** — `model/playback/PlaybackProtocolV3.kt` (358 lines, the
   largest DTO file) and `PlaybackModels.kt`. It has the strongest fence: the server golden
   fixtures and the client conformance test already exist, so a mapping mistake shows up as a
   failed decode, not a runtime bug. Realtime envelopes go with it.
2. **Bloem-only engagement types next** — notifications (S-1), promotions (S-2), ambience
   (S-3), remote control (S-5a). These have no v2 counterpart, so generating them first means
   WP-11/WP-21/WP-24 are written against generated types from day one and never acquire a
   hand-written DTO to replace.
3. **Handler-backed catalog surface last, by package file** — catalog, section, personal,
   request, profile, auth/device-login, calendar, diagnostics, download, subtitles, watch
   together, recommendation, onboarding, server. Each needs a registry root per endpoint
   response; several handler responses are anonymous literals today and will need a named
   type on the server side first (a server change, tracked per domain in chunk C4).

### 9.2 What stays hand-written, and how it is made original

- **Custom serializers.** `FrameRateSerializer.kt` (v3 `model/catalog/FrameRateSerializer.kt`,
  a reflow of v2 per the audit `:142`) is the pattern: a wire value with two shapes. It is
  re-authored under a new name (`model/wire/FrameRateWire.kt`) with its own decomposition, and
  the registry `serializers` entry points the generated `@Serializable(with = …)` at it. The
  generator never emits serializer bodies; the client owns them and the identity audit covers
  them like any other file.
- **Presentation and policy helpers** that v2 put in DTO files (`CatalogModels.kt`
  presentation helpers, `SubtitleTransition`, `AutoSubtitleResolver`, the settings resolution
  files, `PlaybackV3Validation` sealed hierarchy at v2 `PlaybackProtocolV3.kt:457-461`). They
  become extension functions or separate classes over the generated types, written fresh under
  R-31/R-32 — the generated DTO removes the temptation to keep v2's field block as a starting
  point, because there is no field block to edit.
- **Defaults that were client policy** (`seekRestoration = "player_position"`,
  `canSeekAnywhere = true` in v2 `PlaybackProtocolV3.kt:276-277`): the generator emits Go zero
  values; the policy lives in the hand-written consumer that previously relied on the default.
- **Everything with `fun`.** Generated files have none, by test.

## 10. Work breakdown

Each chunk is one agent, one worktree, commit locally; acceptance is the gate for the next.

| # | Chunk | Deliverable | Acceptance |
|---|---|---|---|
| C1 | Registry + type graph | `contracts/client/v1/registry.json` schema + loader; `cmd/clientdtogen/internal/graph`: go/packages load, root resolution, transitive walk, direction propagation, tag parsing, alias/embedded resolution, refusal rules. No emitter. | Unit tests over the synthetic fixture package cover every row of §4.1; refusal tests pass; `go vet`, `golangci-lint` clean. |
| C2 | Kotlin emitter | `-lang kotlin`: file-per-package, header, naming rule, value-class enums, defaults rule, `GeneratedContract.kt`, imports, determinism. | Golden files under `testdata/kotlin/` committed; provenance assertions (§8) pass; naming table test passes; two runs byte-identical. |
| C3 | Pipeline wiring | Registry populated for `internal/playback` (protocol + realtime); `make client-dtos` / `make verify-client-dtos`; committed output under `contracts/client/v1/kotlin/`; CI step beside `verify-settings-bindings`; `SILO_ANDROID_DIR` renamed to `BLOEM_ANDROID_DIR` pointing at the v3 layout; `docs/architecture/client-dto-contract.md` distilled from this spec (AGENTS.md docs hygiene, `:97-105`). | CI green with the new step; round-trip fixture test passes for every playback root; `make verify-local-paths` clean. |
| C4 | Registry coverage | Roots for handlers (capability, items/browse/sections/personal/request/profile/auth/…), notifications, promotions, ambience, remote; named types introduced server-side where a response is an anonymous literal today, each in its own commit with the handler test updated; `serializers` entries for the frame-rate case. Coverage-drift warnings reduced to an explicit allowlist. | Every v2 model file in §9.1 has a generated counterpart or a written reason in the allowlist; digest test wired to the v1-scope removals table. |
| C5 | Client vendoring (WP-05 in bloem-android-v3) | `kotlin-generated` source set, pin file + `verifyContractPin`, conformance round-trip through generated classes, migration of domain group 1 (playback) with hand-written survivors re-authored. | `./gradlew :core:build` green; conformance tests decode every vendored fixture through generated types; identity audit reports zero non-wire shared lines in the touched files; the replaced hand-written files are deleted in the same commits. |
| C6 | Client migration groups 2–3 | Per §9.1, one domain per commit. | Same bar as C5 per domain; WP-05 acceptance "≥ 60 % of v2's mechanical DTO mass generated" (`WP-05-core-dto-codegen.md:38`) measured and reported. |
| C7 | Swift emitter | `-lang swift` beside `-lang kotlin`, over the same registry, graph and naming rules (§4.5): `internal/emit/swift`, one file per Go package under a caseless-enum namespace, `Codable` structs with explicit `CodingKeys`, `RawRepresentable` vocabulary types, the §4.4 defaults applied in a generated `init(from:)`/`encode(to:)`, `GeneratedContract.swift`, the same `// Server revision:` stamp; committed output under `contracts/client/v1/swift/`; `make client-dtos` and `make verify-client-dtos` cover both languages, and the CI drift step gains Swift for free. | Nine criteria, below. |

C1→C2→C3 are sequential; C4 can start after C1; C5 needs C3; C6 needs C4 and C5. C7 needs
C3 and gates the Apple client's WP-05/A; it is independent of C4 and C6.

### 10.1 C7 acceptance criteria

The answer to §11 Q8 created this chunk without one. It is met when all nine hold:

1. **Kotlin output is byte-identical.** Regenerate and diff `contracts/client/v1/kotlin/` and the
   Android vendored copy before and after; both unchanged, `CONTRACT_DIGEST` included. Android's
   pinned contract does not move for a Swift change.
2. **Golden files** under `cmd/clientdtogen/testdata/swift/` are committed, and two emits over the
   fixture graph are byte-identical.
3. **Provenance assertions** over the golden output, the Swift reading of §2: every declaration is
   explicitly `public` bar the private indirection box, the only `func` is the mechanical
   `encode(to:)`, every type name is a Go type name, and every property has its own `CodingKeys`
   case carrying the wire name unchanged.
4. **The generated Swift compiles.** `swiftc -swift-version 6` type-checks both the fixture goldens
   and the whole committed `contracts/client/v1/swift/` tree against the client-owned support types.
   The committed-tree check is not optional: the real registry reaches shapes the synthetic fixture
   does not.
5. **A runtime conformance driver** decodes bodies the Go types can produce and asserts §4.4 holds:
   an empty object takes every documented default, a `null` list coerces to empty, an unknown key
   and an unknown enum value both survive, a self-referential type round-trips to an equal value
   with no `null` written for its absent tail, and a request-only type refuses a body missing a
   required key.
6. **The round-trip gate covers both clients.** Every wire key of every registered playback root —
   from the golden fixtures and from the marshaled realtime roots — is a `CodingKeys` case in the
   committed Swift as well as an `@SerialName` in the committed Kotlin. Mutation-tested: deleting
   one case fails the test and names the field, the type and the file.
7. **`make verify-client-dtos` regenerates each language against the revision stamped in its own
   committed contract file**, never `HEAD`, so committed output can never make itself stale.
8. **Refusals name the offender**: a serializer entry with no usable target, a constant colliding
   with `KNOWN`, a type whose name is a namespace, a `Protocol`/`Self`/`Any` type referenced across
   namespaces, and a containment cycle with no pointer in it.
9. **The divergences are written down** in §4.5 rather than discovered by the client.

Two follow-ups are deliberately **not** gating:

- Explicit `swift` targets in the registry's `serializers` map. Adding one changes the graph dump
  and therefore `CONTRACT_DIGEST`, which is an Android re-vendor for a change that alters no wire
  shape; do it the next time the digest is allowed to move.
- The `make client-dtos` copy hook into a sibling `bloem-apple-v3` checkout, the twin of
  `BLOEM_ANDROID_DTO_DIR`. It lands with WP-05/A, which is what decides where the client's
  generated source directory lives; guessing the path here would be a doc that disagrees with the
  code.


## 11. Open questions for the owner

Each with the default the implementation takes if unanswered.

1. **Property identifier rule.** Default: camelCase from the Go field name with the initialism
   rule in §4.3 (identifiers can coincide with v2's where Go and wire words agree; declaration
   lines never do). Alternative: Go-identical `PascalCase` properties
   (`public val SourceStartSeconds: Double`) — identifier-level divergence from v2 in every
   case and grep-identical names across server and client, at the cost of unidiomatic Kotlin
   call sites throughout the client.
2. **String enumerations as value classes** with a `KNOWN` table (default, tolerant of new
   server vocabulary) versus `enum class` (closed, exhaustive `when`, but a new server constant
   fails decoding of every response type that carries it). If the owner wants exhaustiveness for
   request-only enums, the generator can emit `enum class` for types that are not
   response-reachable.
3. **`time.Time` as `String`** (default) versus `kotlinx.datetime.Instant` with a generated
   `@Serializable(with = …)`. Default keeps generated code dependency-free and correct for the
   hand-formatted date fields; parsing is one hand-written extension in the client.
4. **Handlers output granularity.** Default: one `Handlers.kt` per the file-per-Go-package
   ruling, even though `internal/api/handlers` is by far the largest package. Alternative:
   split that one package by Go source file (`Handlers.items.kt`, …), keeping every other
   package whole.
5. **Untagged exported fields**: error (default) versus applying encoding/json's Go-name
   fallback. Default forces the server to state its wire names.
6. **Where the server commits its output copy.** Default `contracts/client/v1/kotlin/`, next
   to the registry, mirroring how settings bindings are committed in-repo. Alternative: no
   in-repo copy and a client-checkout diff — rejected in §6.3 but listed for completeness.
7. **Generator packaging.** Default: a sibling command `cmd/clientdtogen` sharing the Makefile
   "bindings" family and header conventions with `cmd/settingsgen`, because the inputs differ
   (Go packages versus a JSON manifest) and `settingsgen` already has four emitters in one
   file. Alternative: a `-mode dto` flag inside `cmd/settingsgen`, which reads R-19's "extend the
   settings-generator pipeline" literally.
8. **Swift emission.** Default: out of scope for this spec; the graph/emitter split in C1/C2
   keeps a `-lang swift` emitter possible for bloem-apple without touching the registry or the
   walk. Say now if bloem-apple must be covered in the same program so C2 designs the emitter
   interface for two targets from the start.
9. **Bloem-only field marking in upstream-compat types.** Default: registry `bloem_fields`
   lists (documentation + dialect table only; decoding does not depend on them). Alternative:
   struct tags on the Go side — rejected because it edits upstream-identical files.

### Owner answers (2026-09-02)

- Q1 property identifier rule: **camelCase from the Go field name** (default). Declaration lines
  are what the provenance gate measures; identifier coincidence with v2 is acceptable.
- Q8 Swift emission: **YES — bloem-apple is in scope for the same generator.** C2 designs the
  emitter interface for two targets from the start (`-lang kotlin|swift`); the Swift emitter itself
  is a later chunk (C7) after C3, with the same provenance rule vs the v2 apple client.
  C7 is written up in §10 with acceptance criteria in §10.1, and the mapping decisions Swift
  forces are in §4.5.
- All other questions: the stated defaults.
