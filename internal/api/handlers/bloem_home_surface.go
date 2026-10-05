package handlers

import "github.com/Silo-Server/silo-server/internal/promotions"

// Fixed at package initialization; request handlers only read this registry.
var homeSurfaceExtensions = []func(string) bool{promotions.IsDismissalSurface}
