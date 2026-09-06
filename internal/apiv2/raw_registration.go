package apiv2

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
)

// RawOperation describes a finite raw HTTP handshake in the native OpenAPI
// artifact. The owning transport enforces its declared path/query/header
// constraints and range, HEAD, conditional, and media authorization semantics.
// Structured JSON operations must use Register instead.
type RawOperation struct {
	Operation
	Protocol string
	Reason   string
}

// RawImpliedStatuses lists only failures of the shared authorization gates.
// Raw handlers declare their own protocol statuses; JSON negotiation and Huma
// input validation do not run on byte streams.
func RawImpliedStatuses(class Class, serviceBacked bool) []int {
	if class == ClassPublic {
		if serviceBacked {
			return []int{http.StatusServiceUnavailable}
		}
		return nil
	}
	statuses := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable}
	if resolvesProfile(class) {
		statuses = append(statuses, http.StatusNotFound, http.StatusUnprocessableEntity)
	}
	slices.Sort(statuses)
	return statuses
}

// RegisterRaw registers a GET/HEAD byte-stream handshake and its actual wire
// documentation. No Huma JSON validation, negotiation, encoding, or buffering
// runs here. Missing authorization dependencies still fail closed.
func RegisterRaw(reg *Registry, raw RawOperation, handler http.Handler) {
	op := raw.Operation
	fail := func(message string) { panic(fmt.Sprintf("apiv2: %s: %s", op.OperationID, message)) }
	if err := checkOperation(op); err != nil {
		fail(err.Error())
	}
	if handler == nil {
		fail("raw handler is required")
	}
	if op.Method != http.MethodGet && op.Method != http.MethodHead {
		fail("raw registration currently supports GET and HEAD handshakes")
	}
	if strings.TrimSpace(raw.Protocol) == "" || strings.TrimSpace(raw.Reason) == "" {
		fail("raw protocol and exclusion reason are required")
	}
	if op.MaxBodyBytes != 0 || op.BodyReadTimeout != 0 || op.RequestBody != nil || op.Guarded || op.CreateOnly || op.Conditional || op.Deprecation != nil || len(op.Errors) != 0 {
		fail("raw handlers must explicitly document and enforce protocol controls; structured body/concurrency/deprecation options are unsupported")
	}
	if len(op.Responses) == 0 {
		fail("raw response statuses and media types are required")
	}
	success := false
	for status, response := range op.Responses {
		code, err := strconv.Atoi(status)
		if err != nil || code < 100 || code > 599 || response == nil || response.Description == "" {
			fail("raw responses require explicit statuses and descriptions")
		}
		if code >= 200 && code < 300 {
			success = true
			for media := range response.Content {
				media = strings.ToLower(strings.TrimSpace(strings.SplitN(media, ";", 2)[0]))
				if media == mediaTypeJSON || strings.HasSuffix(media, "+json") {
					fail("JSON success responses must use Huma Register")
				}
			}
			if op.Method == http.MethodGet && code != http.StatusNoContent && len(response.Content) == 0 {
				fail("raw GET success requires its media type")
			}
		}
	}
	if !success {
		fail("raw success response is required")
	}
	// Refuse missing path documentation instead of inventing parameter schemas.
	for _, part := range strings.Split(op.Path, "/") {
		if !strings.HasPrefix(part, "{") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		if !slices.ContainsFunc(op.Parameters, func(p *huma.Param) bool {
			return p != nil && p.In == "path" && p.Name == name && p.Required && p.Schema != nil
		}) {
			fail("raw path parameter must be explicitly documented: " + name)
		}
	}
	for _, declared := range reg.Declared() {
		if declared.Method == op.Method && declared.Path == op.Path {
			fail("duplicate raw route")
		}
	}
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[metaClass] = op.Class
	op.Metadata[metaPermission] = op.Permission
	op.Metadata[metaDemoRestricted] = op.DemoRestricted
	op.Metadata[metaProfileOptional] = op.ProfileOptional
	op.Metadata[metaRateLimitBucket] = op.RateLimitBucket
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions[extClass] = string(op.Class)
	if op.Permission != "" {
		op.Extensions[extPermission] = op.Permission
	}
	if op.ServiceBacked {
		op.Extensions[extServiceBacked] = true
	}
	if op.DemoRestricted {
		op.Extensions[extDemoRestricted] = true
	}
	op.Extensions["x-silo-raw-protocol"] = raw.Protocol
	op.Extensions["x-silo-raw-reason"] = raw.Reason
	if op.Class != ClassPublic {
		op.Security = []map[string][]string{{securitySchemeBearer: {}}}
	}
	if resolvesProfile(op.Class) {
		class := op.Class
		if op.ProfileOptional {
			class = ClassAuthenticated
		}
		op.Parameters = append(op.Parameters, profileHeaderParam(class), profileTokenHeaderParam())
	}
	for _, status := range RawImpliedStatuses(op.Class, op.ServiceBacked) {
		key := strconv.Itoa(status)
		if _, exists := op.Responses[key]; !exists {
			op.Responses[key] = &huma.Response{Description: http.StatusText(status), Content: map[string]*huma.MediaType{
				problemContentType: {Schema: reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "")},
			}}
		}
	}
	reg.api.OpenAPI().AddOperation(&op.Operation)
	reg.mu.Lock()
	reg.ops = append(reg.ops, Declared{Method: op.Method, Path: op.Path, OperationID: op.OperationID, Class: op.Class})
	reg.mu.Unlock()
	serve := func(ctx huma.Context) {
		r, w := humachi.Unwrap(ctx)
		handler.ServeHTTP(w, r.WithContext(ctx.Context()))
	}
	for _, middleware := range []func(huma.Context, func(huma.Context)){observeIdentity, classGate(reg.deps), observeOperation} {
		next := serve
		serve = func(ctx huma.Context) { middleware(ctx, next) }
	}
	reg.api.Adapter().Handle(&op.Operation, serve)
}
