package handlers

// NativeAPIPrefix is the single place the Bloem-native surface's path is
// spelled. /api/v2 is upstream Silo's native API, so the Bloem-native surface
// sits behind a vendor segment with its own version line. Spelling it once keeps a later move — or serving this
// prefix from a different router — a change at one site.
//
// It lives in handlers rather than in the router package because handlers that
// build self-referential links need it too, and handlers cannot import the
// router package that imports them.
const NativeAPIPrefix = "/api/bloem/v2"
