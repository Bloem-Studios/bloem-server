package scenariocatalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	apiv2 "github.com/Silo-Server/silo-server/contracts/api/v2"
)

const bindingAPIKeyPrincipal = "api_key"
const capturedIfMatch = "If-Match"
const MaxV2Exchanges = 16
const MaxResponseBindings = 8
const MaxCapturedStringBytes = 16384

var captureName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// ValidateV2Sequence also protects manually constructed executor inputs.
func ValidateV2Sequence(pair *V2Expectation) error {
	if pair == nil {
		return fmt.Errorf("missing v2 expectation")
	}
	count := max(1, pair.Request.Repeat)
	if count > MaxV2Exchanges {
		return fmt.Errorf("v2 sequence exceeds %d exchanges", MaxV2Exchanges)
	}
	for i, step := range pair.Then {
		if step.Request.Body != nil && (step.Request.BodyRef != "" || step.Request.RawBody != nil || step.Request.Multipart != nil) {
			return fmt.Errorf("then[%d]: multiple body sources", i)
		}
		if err := validateOperation(step.OperationID, step.Method, step.Request.Path); err != nil {
			return fmt.Errorf("then[%d]: %w", i, err)
		}
		if step.Principal != nil && step.Principal.Class != bindingAPIKeyPrincipal && len(step.Principal.Scopes) > 0 {
			return fmt.Errorf("then[%d]: scopes require API-key principal", i)
		}
		if err := ValidateV2Bindings(step); err != nil {
			return fmt.Errorf("then[%d]: %w", i, err)
		}
		if max(1, step.Request.Repeat) > MaxV2Exchanges-count {
			return fmt.Errorf("v2 sequence exceeds %d exchanges", MaxV2Exchanges)
		}
		count += max(1, step.Request.Repeat)
	}
	return nil
}

// ValidateV2Bindings restricts query destinations to declared string parameters
// of this operation. Integer/body/path capture is outside this checkpoint.
func ValidateV2Bindings(step V2Step) error {
	if err := ValidateResponseBindings(step.Request, step.FromPrevious); err != nil {
		return err
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					Type string `json:"type"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(apiv2.OpenAPI, &spec); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, methods := range spec.Paths {
		for _, op := range methods {
			if op.OperationID == step.OperationID {
				for _, p := range op.Parameters {
					if p.In == "query" && p.Schema.Type == "string" {
						allowed[p.Name] = true
					}
				}
			}
		}
	}
	for _, b := range step.FromPrevious {
		if b.Query != "" && !allowed[b.Query] {
			return fmt.Errorf("captured query is not a declared string parameter of %s", step.OperationID)
		}
	}
	return nil
}

func ValidateResponseBindings(request Request, bindings []ResponseBinding) error {
	if len(bindings) > MaxResponseBindings {
		return fmt.Errorf("too many response bindings")
	}
	destinations := map[string]bool{}
	for _, b := range bindings {
		if (b.Header == "") == (b.Pointer == nil) {
			return fmt.Errorf("binding needs exactly one response header or pointer")
		}
		if b.Header != "" && !captureName.MatchString(b.Header) {
			return fmt.Errorf("invalid response header name")
		}
		if b.Pointer != nil && !validCapturePointer(*b.Pointer) {
			return fmt.Errorf("invalid response JSON pointer")
		}
		if (b.RequestHeader == "") == (b.Query == "") {
			return fmt.Errorf("binding needs exactly one request header or query destination")
		}
		target := "query:" + b.Query
		if b.RequestHeader != "" {
			// Only conditional headers are needed by this checkpoint. Credentials,
			// profile selection, framing and destination authority cannot be captured.
			name := http.CanonicalHeaderKey(b.RequestHeader)
			if name != capturedIfMatch && name != "If-None-Match" {
				return fmt.Errorf("unsupported captured request header")
			}
			for k := range request.Headers {
				if strings.EqualFold(k, name) {
					return fmt.Errorf("captured header already specified")
				}
			}
			target = "header:" + name
		} else {
			if !captureName.MatchString(b.Query) || strings.EqualFold(b.Query, "token") {
				return fmt.Errorf("unsupported captured query name")
			}
			if _, ok := request.Query[b.Query]; ok {
				return fmt.Errorf("captured query already specified")
			}
			u, err := url.Parse(request.Path)
			if err != nil {
				return err
			}
			if u.Query().Has(b.Query) {
				return fmt.Errorf("captured query already in path")
			}
		}
		if destinations[target] {
			return fmt.Errorf("duplicate captured destination")
		}
		destinations[target] = true
	}
	return nil
}

func validCapturePointer(pointer string) bool {
	if pointer != "" && !strings.HasPrefix(pointer, "/") {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] == '~' {
			i++
			if i == len(pointer) || (pointer[i] != '0' && pointer[i] != '1') {
				return false
			}
		}
	}
	return true
}
