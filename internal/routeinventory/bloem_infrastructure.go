package routeinventory

// Bloem's base-stack middleware for native sources on core-access transcode
// nodes (internal/api/bloem_native_node_sources.go). On every route but
// Silo's node source bytes route it passes the request on unchanged; on that
// route it answers only a Bloem source grant, after the same node signature
// check Silo's route makes (nodecontrol.RequireNode), and hands every other
// request to Silo's handler. It grants no authorization of its own, so it is
// infrastructure, as Silo's base stack is.
func init() {
	infrastructureMiddleware = append(infrastructureMiddleware, "NativeNodeSources.SourceBytesMiddleware")
}
