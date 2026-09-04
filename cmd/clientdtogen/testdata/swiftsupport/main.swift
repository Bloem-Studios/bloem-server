// The Swift half of the round-trip gate (docs/specs/client-dto-generator.md
// §8, §10 C7): it decodes bodies the Go types can produce through the
// generated fixture structs and checks the §4.4 rules hold at runtime rather
// than only in the golden text. cmd/clientdtogen/internal/emit/swift compiles
// and runs this against the committed goldens.
import Foundation

/// Stands in for the client-owned registry serializer the fixture registry
/// points `frame_rate` at.
public struct FrameRateWire: Codable, Hashable, Sendable {
    public let value: Double
}

nonisolated(unsafe) var failures: [String] = []

func check(_ ok: Bool, _ what: String) {
    if !ok { failures.append(what) }
}

let decoder = JSONDecoder()
let encoder = JSONEncoder()

func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
    try decoder.decode(type, from: Data(json.utf8))
}

// An empty response body: every response-reachable field takes its documented
// default, which is what lets an upstream-compat server omit every Bloem key.
do {
    let s = try decode(Fixture.Scalars.self, "{}")
    check(s.i64 == 0, "i64 default")
    check(s.f64 == 0.0, "f64 default")
    check(s.b == false, "b default")
    check(s.u == "", "u default")
    check(s.tp == nil, "tp default")
    check(s.raw == nil, "raw default")
    check(s.anyMap.isEmpty, "anyMap default")
    check(s.names.isEmpty, "names default")
    check(s.proto.wire == "", "proto default")
    check(s.protoPtr == nil, "protoPtr default")
} catch {
    failures.append("empty Scalars body did not decode: \(error)")
}

// A nil Go slice marshals as null, not []. Kotlin coerces it with
// coerceInputValues; decodeIfPresent has to do the same or every list field
// would throw against a real server response.
do {
    let c = try decode(Fixture.Collections.self, #"{"strings": null, "headers": null, "nested": null}"#)
    check(c.strings.isEmpty, "null list coerced to empty")
    check(c.headers.isEmpty, "null map coerced to empty")
    check(c.nested.isEmpty, "null nested list coerced to empty")
} catch {
    failures.append("null collections did not decode: \(error)")
}

// An unknown key and an unknown enum value are both tolerated: the server may
// be newer than the client.
do {
    let s = try decode(Fixture.Scalars.self, #"{"proto": "quic-someday", "i64": 9007199254740993, "unheard_of": 1}"#)
    check(s.proto.wire == "quic-someday", "unknown enum value survives")
    check(s.proto != .HLS && s.proto != .PROGRESSIVE && s.proto != .LATE, "unknown value is in no known constant")
    check(s.i64 == 9_007_199_254_740_993, "int64 keeps full precision")
} catch {
    failures.append("tolerant Scalars body did not decode: \(error)")
}

// The self-referential type: Go writes a chain, Swift reads one, and
// re-encoding omits the absent tail rather than writing null.
do {
    let c = try decode(Fixture.Child.self, #"{"name":"a","next":{"name":"b","next":{"name":"c"}}}"#)
    check(c.next?.next?.name == "c", "recursive decode")
    check(c.next?.next?.next == nil, "recursion terminates")
    let round = String(decoding: try encoder.encode(c), as: UTF8.self)
    check(!round.contains("null"), "absent recursive tail is omitted, not null")
    let back = try decode(Fixture.Child.self, round)
    check(back == c, "recursive value round-trips to an equal value")
} catch {
    failures.append("recursive Child did not decode: \(error)")
}

// Request-only types keep required fields: a compile error on the client, and
// a decode error here, is the right signal when a key is missing.
do {
    _ = try decode(Fixture.Embedded.self, #"{"deep":{"depth":1},"kind":"k","extra":"e","-":"d","mixed":"m","low":"l","direct":{"name":"n"}}"#)
    failures.append("a request-only type accepted a body with no id")
} catch {
    // expected
}

// Wire names are the json tags, byte for byte, and are the only place the
// generated Swift may coincide with hand-written client code.
do {
    let encoded = String(decoding: try encoder.encode(Fixture.Scalars()), as: UTF8.self)
    check(encoded.contains("\"any_map\""), "wire name any_map is written")
    check(encoded.contains("\"proto_omit\""), "wire name proto_omit is written")
    check(!encoded.contains("\"int_ptr\""), "an absent optional writes no key")
} catch {
    failures.append("Scalars did not encode: \(error)")
}

if failures.isEmpty {
    print("swift conformance: ok")
} else {
    for failure in failures { print("swift conformance FAILED: \(failure)") }
    exit(1)
}
