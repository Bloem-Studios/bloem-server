// Reference implementation of the two hand-written types the generated Swift
// refers to (docs/specs/client-dto-generator.md §9.2, §10 C7). The generator
// never emits them: the client owns them, the way it owns the registry
// serializers. The Swift emitter test compiles the generated fixture against
// this file, so a change here that breaks the generated code fails in the
// server repo rather than in the client's.
//
// bloem-apple-v3 is expected to copy this file (or write its own equivalent
// satisfying the same three requirements: Codable, Hashable, Sendable).

/// A decoded JSON value, the Swift counterpart of kotlinx.serialization's
/// JsonElement. `json.RawMessage` and `any` fields decode into it.
public enum BloemJSONValue: Codable, Hashable, Sendable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([BloemJSONValue])
    case object([String: BloemJSONValue])

    public init(from decoder: any Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([BloemJSONValue].self) {
            self = .array(value)
        } else if let value = try? container.decode([String: BloemJSONValue].self) {
            self = .object(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "not a JSON value")
        }
    }

    public func encode(to encoder: any Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .null: try container.encodeNil()
        case .bool(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .string(let value): try container.encode(value)
        case .array(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        }
    }
}

/// A reference-backed box. Swift stores a struct inline, so a wire type that
/// can contain itself cannot hold the field directly; the generator routes
/// such fields through this box and exposes them as the honest type.
public struct Indirect<Value> {
    private final class Box {
        let value: Value
        init(_ value: Value) { self.value = value }
    }

    private let box: Box

    public var value: Value { box.value }

    public init(_ value: Value) { self.box = Box(value) }
}

extension Indirect: Equatable where Value: Equatable {
    public static func == (lhs: Indirect, rhs: Indirect) -> Bool { lhs.value == rhs.value }
}

extension Indirect: Hashable where Value: Hashable {
    public func hash(into hasher: inout Hasher) { hasher.combine(value) }
}

extension Indirect: @unchecked Sendable where Value: Sendable {}
