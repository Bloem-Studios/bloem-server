package routeinventory

import "testing"

func TestAnalyzeHandlerHelperKinds(t *testing.T) {
	inv := analyzeFixture(t, "handler_kinds")
	want := map[string][2]string{
		"/wrapped":           {KindNone, KindBinary},
		"/closure":           {KindNone, KindRedirect},
		"/uncalled":          {KindNone, unknownClassification},
		"/guarded":           {KindJSON, KindJSON},
		"/fresh-request":     {KindNone, unknownClassification},
		"/method-expression": {KindNone, KindRedirect},
		"/upstream-decoded":  {KindNone, KindJSON},
		"/socket":            {KindNone, KindWebSocket},
		"/upload":            {KindMultipart, unknownClassification},
		"/redirect":          {KindNone, KindRedirect},
		"/download":          {KindNone, KindBinary},
		"/binary":            {KindBinary, unknownClassification},
		"/json":              {KindJSON, KindJSON},
		"/upstream-json":     {KindNone, unknownClassification},
		"/upstream-headers":  {KindNone, unknownClassification},
		"/events":            {KindNone, KindEventSt},
		"/recursive":         {KindNone, KindBinary},
		"/other-receiver":    {KindNone, KindJSON},
		"/literal":           {KindJSON, KindJSON},
	}
	if len(inv.Routes) != len(want) {
		t.Fatalf("got %d routes, want %d", len(inv.Routes), len(want))
	}
	for _, route := range inv.Routes {
		t.Run(route.Path, func(t *testing.T) {
			expected, ok := want[route.Path]
			if !ok {
				t.Fatalf("unexpected route %s", route.Path)
			}
			if got := [2]string{route.RequestKind, route.ResponseMediaKind}; got != expected {
				t.Errorf("request/response kinds = %v, want %v", got, expected)
			}
			if route.UpgradesWebSocket != (expected[1] == KindWebSocket) {
				t.Errorf("websocket = %v", route.UpgradesWebSocket)
			}
		})
	}
}
