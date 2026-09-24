package notifications

// DefaultPushRelayURL (settings.go) must be Bloem's own relay, never Silo's.
// Registering with push.siloserver.org would put this deployment's device
// tokens and delivery metadata through infrastructure Silo controls, delivered
// on Silo's APNs/FCM credentials — the exact boundary Bloem's own relay
// (Bloem-Studios/bloem-push-relay) exists to keep. NormalizePushRelayURL
// enforces this value as the only allowed origin bar an explicit development
// override, so changing it changes where every self-hosted Bloem server
// registers.
