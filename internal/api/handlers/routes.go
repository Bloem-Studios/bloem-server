package handlers

// NativeAPIPrefix is the single place the Bloem-native surface's path is
// spelled. /api/v1 is the Silo-compatible projection and /api/v2 is reserved
// for upstream Silo, so the native surface sits behind a vendor segment with
// its own version line. Spelling it once keeps a later move — or serving this
// prefix from a different router — a change at one site.
//
// It lives in handlers rather than in the router package because handlers that
// build self-referential links need it too, and handlers cannot import the
// router package that imports them.
const NativeAPIPrefix = "/api/bloem/v1"
