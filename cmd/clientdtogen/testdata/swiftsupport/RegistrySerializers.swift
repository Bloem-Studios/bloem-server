// Stand-ins for the client-owned types the real registry's `serializers`
// entries name (docs/specs/client-dto-generator.md §9.2). They exist so the
// emitter test can type-check the committed contracts/client/v1/swift tree
// without a client checkout; bloem-apple-v3 writes the real ones.
//
// A new `serializers` entry in contracts/client/v1/registry.json needs a
// stand-in here, or the committed-tree compile fails naming the missing type.

/// contracts/client/v1/registry.json: internal/models.VideoTrack.frame_rate.
public struct FrameRateWire: Codable, Hashable, Sendable {
    public let value: Double
}
