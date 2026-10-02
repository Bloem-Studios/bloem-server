package api

import "github.com/Silo-Server/silo-server/internal/streamtelemetry"

func declaredNativeMediaRoutes() []streamtelemetry.MediaRoute {
	declareNativeMediaRoutes()
	return streamtelemetry.DeclaredRoutes(streamtelemetry.FamilyNative)
}
