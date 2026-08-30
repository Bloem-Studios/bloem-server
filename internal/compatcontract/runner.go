package compatcontract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func run(ctx context.Context, target Target, suite Suite) (Report, error) {
	base, err := parseTarget(target.BaseURL)
	report := Report{TargetOrigin: targetOrigin(base)}
	if err != nil {
		return report, err
	}
	if suite.Name == "" {
		return report, errors.New("compatibility contract suite has no name")
	}
	if target.Client == nil {
		target.Client = http.DefaultClient
	}

	var runErrs []error
	for _, c := range suite.Cases {
		result := CaseResult{Name: c.Name}
		started := time.Now()
		var caseErr error
		if len(c.WantWebSocketJSON) > 0 || c.WantWebSocketFiltered {
			caseErr = runWebSocketCase(ctx, target, base, c, &result)
		} else {
			caseErr = runHTTPCase(ctx, target, base, c, &result)
		}
		if caseErr == nil && c.Timing != nil {
			caseErr = checkTimingDistribution(ctx, target, base, c)
		}
		result.Duration = time.Since(started)
		result.Passed = caseErr == nil
		if caseErr != nil {
			result.Error = redactError(caseErr)
			runErrs = append(runErrs, fmt.Errorf("%s: %w", c.Name, caseErr))
		}
		report.Results = append(report.Results, result)
	}
	return report, errors.Join(runErrs...)
}

func parseTarget(raw string) (*url.URL, error) {
	base, err := url.Parse(raw)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return base, errors.New("compatibility contract target must be an absolute HTTP URL")
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return base, errors.New("compatibility contract target must use HTTP or HTTPS")
	}
	if base.User != nil || hasSensitiveQuery(base.Query()) {
		return base, errors.New("compatibility contract target includes credentials or a sensitive query parameter")
	}
	return base, nil
}

func targetOrigin(base *url.URL) string {
	if base == nil || base.Scheme == "" || base.Host == "" {
		return ""
	}
	return base.Scheme + "://" + base.Host
}

func hasSensitiveQuery(values url.Values) bool {
	for key := range values {
		key = strings.ToLower(key)
		if strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "password") || strings.Contains(key, "authorization") || strings.Contains(key, "cookie") || key == "key" {
			return true
		}
	}
	return false
}

var bindingPattern = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

// expandBindings substitutes {{name}} placeholders from bindings. An unbound
// placeholder is an error so a fixture cannot silently probe a literal
// "{{token}}" and mistake the refusal for the behavior under test.
func expandBindings(value string, bindings map[string]string) (string, error) {
	var missing []string
	expanded := bindingPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := match[2 : len(match)-2]
		bound, ok := bindings[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return bound
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unbound fixture placeholder %q", missing[0])
	}
	return expanded, nil
}

func expandStrings(values []string, bindings map[string]string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	expanded := make([]string, 0, len(values))
	for _, value := range values {
		e, err := expandBindings(value, bindings)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, e)
	}
	return expanded, nil
}

// expandCase resolves every placeholder-bearing field of a case against the
// target's bindings, returning a fully concrete copy.
func expandCase(c Case, bindings map[string]string) (Case, error) {
	var err error
	if c.Path, err = expandBindings(c.Path, bindings); err != nil {
		return c, err
	}
	if len(c.BodyJSON) > 0 {
		expanded, expandErr := expandBindings(string(c.BodyJSON), bindings)
		if expandErr != nil {
			return c, expandErr
		}
		c.BodyJSON = json.RawMessage(expanded)
	}
	if len(c.Headers) > 0 {
		headers := make(map[string]string, len(c.Headers))
		for key, value := range c.Headers {
			if headers[key], err = expandBindings(value, bindings); err != nil {
				return c, err
			}
		}
		c.Headers = headers
	}
	if c.PresentStrings, err = expandStrings(c.PresentStrings, bindings); err != nil {
		return c, err
	}
	if c.AbsentStrings, err = expandStrings(c.AbsentStrings, bindings); err != nil {
		return c, err
	}
	if len(c.WantJSONFields) > 0 {
		fields := make(map[string]json.RawMessage, len(c.WantJSONFields))
		for selector, want := range c.WantJSONFields {
			expanded, expandErr := expandBindings(string(want), bindings)
			if expandErr != nil {
				return c, expandErr
			}
			fields[selector] = json.RawMessage(expanded)
		}
		c.WantJSONFields = fields
	}
	if c.Timing != nil {
		timing := *c.Timing
		if timing.ControlPath, err = expandBindings(timing.ControlPath, bindings); err != nil {
			return c, err
		}
		c.Timing = &timing
	}
	return c, nil
}

func caseBody(c Case) []byte {
	if len(c.BodyJSON) > 0 {
		return []byte(c.BodyJSON)
	}
	return c.Body
}

func runHTTPCase(ctx context.Context, target Target, base *url.URL, c Case, result *CaseResult) error {
	c, err := expandCase(c, target.Bindings)
	if err != nil {
		return err
	}
	method := c.Method
	if method == "" {
		method = http.MethodGet
	}
	caseURL, err := resolveFixtureURL(base, c.Path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, caseURL.String(), bytes.NewReader(caseBody(c)))
	if err != nil {
		return errors.New("build request")
	}
	if len(c.BodyJSON) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if target.Credentials != nil {
		if err := target.Credentials.Apply(req); err != nil {
			return errors.New("apply credentials")
		}
	}
	for key, value := range c.Headers {
		req.Header.Set(key, value)
	}

	resp, err := target.Client.Do(req)
	if err != nil {
		return errors.New("send request")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errors.New("read response")
	}
	result.Status = resp.StatusCode
	// Captures run before assertions so a case that fails its own contract
	// still hands the credential it was issued to the cases that must prove
	// the credential is bounded.
	if err := captureBindings(c, body, target.Bindings); err != nil {
		return err
	}
	if err := checkCaseResponse(c, resp, body); err != nil {
		return err
	}
	return nil
}

func captureBindings(c Case, body []byte, bindings map[string]string) error {
	if len(c.Capture) == 0 {
		return nil
	}
	if bindings == nil {
		return errors.New("case captures a binding but the target has no bindings map")
	}
	for name, selector := range c.Capture {
		value, err := jsonValueAt(body, selector)
		if err != nil {
			return fmt.Errorf("capture %s: %w", name, err)
		}
		switch typed := value.(type) {
		case string:
			bindings[name] = typed
		case float64:
			bindings[name] = strconv.FormatFloat(typed, 'f', -1, 64)
		case bool:
			bindings[name] = strconv.FormatBool(typed)
		default:
			return fmt.Errorf("capture %s: selector does not resolve to a scalar", name)
		}
	}
	return nil
}

func checkCaseResponse(c Case, resp *http.Response, body []byte) error {
	if c.Exception != "" {
		if !matchesException(c.Exception, resp.StatusCode) {
			return fmt.Errorf("status %d does not match %s exception", resp.StatusCode, c.Exception)
		}
	} else if c.WantStatus != 0 && resp.StatusCode != c.WantStatus {
		return fmt.Errorf("status %d, want %d", resp.StatusCode, c.WantStatus)
	}
	for key, want := range c.WantHeaders {
		if got := resp.Header.Get(key); got != want {
			return fmt.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
	if len(c.WantJSON) > 0 && !sameJSON(c.WantJSON, body) {
		return errors.New("response JSON does not match fixture")
	}
	for selector, want := range c.WantJSONCounts {
		got, err := jsonElementCount(body, selector)
		if err != nil {
			return fmt.Errorf("count %s: %w", selector, err)
		}
		if got != want {
			return fmt.Errorf("count %s = %d, want %d", selector, got, want)
		}
	}
	for selector, want := range c.WantJSONFields {
		got, err := jsonValueAt(body, selector)
		if err != nil {
			return fmt.Errorf("field %s: %w", selector, err)
		}
		var wantValue any
		if err := json.Unmarshal(want, &wantValue); err != nil {
			return fmt.Errorf("field %s: fixture value is not JSON", selector)
		}
		if !reflect.DeepEqual(wantValue, got) {
			return fmt.Errorf("field %s does not match the fixture value", selector)
		}
	}
	if c.WantSHA256 != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(c.WantSHA256, hex.EncodeToString(sum[:])) {
			return errors.New("response SHA-256 does not match fixture")
		}
	}
	for _, required := range c.PresentStrings {
		if required != "" && !bytes.Contains(body, []byte(required)) {
			return errors.New("response omits a required fixture identifier")
		}
	}
	for _, forbidden := range c.AbsentStrings {
		if forbidden != "" && bytes.Contains(body, []byte(forbidden)) {
			return errors.New("response contains an excluded fixture identifier")
		}
	}
	return nil
}

func matchesException(name string, status int) bool {
	switch name {
	case ExceptionUnauthenticated:
		return status == http.StatusUnauthorized || status == http.StatusForbidden
	case ExceptionNotFound:
		return status == http.StatusNotFound
	case ExceptionInvalidRequest:
		return status == http.StatusBadRequest
	default:
		return false
	}
}

// jsonValueAt resolves a dotted selector ("$", "$.Items", "$.0.Name") against
// body. Numeric segments index arrays; other segments traverse objects. A
// missing field is an error rather than a nil value.
func jsonValueAt(body []byte, selector string) (any, error) {
	if selector != "$" && !strings.HasPrefix(selector, "$.") {
		return nil, errors.New("selector must start at $")
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, errors.New("response is not JSON")
	}
	if selector == "$" {
		return value, nil
	}
	for _, segment := range strings.Split(selector[2:], ".") {
		if index, err := strconv.Atoi(segment); err == nil {
			array, ok := value.([]any)
			if !ok {
				return nil, errors.New("selector indexes a non-array")
			}
			if index < 0 || index >= len(array) {
				return nil, errors.New("selector index is out of range")
			}
			value = array[index]
			continue
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("selector path does not traverse an object")
		}
		value, ok = object[segment]
		if !ok {
			return nil, errors.New("selector path is absent from the response")
		}
	}
	return value, nil
}

// jsonElementCount returns the length of the array a selector resolves to. A
// missing field or a non-array value is an error rather than a zero count, so
// a contract that promises an empty collection cannot be satisfied by the
// collection disappearing.
func jsonElementCount(body []byte, selector string) (int, error) {
	value, err := jsonValueAt(body, selector)
	if err != nil {
		return 0, err
	}
	array, ok := value.([]any)
	if !ok {
		return 0, errors.New("selector does not resolve to an array")
	}
	return len(array), nil
}

func sameJSON(want, got []byte) bool {
	var wantValue any
	var gotValue any
	return json.Unmarshal(want, &wantValue) == nil && json.Unmarshal(got, &gotValue) == nil && reflect.DeepEqual(wantValue, gotValue)
}

func runWebSocketCase(ctx context.Context, target Target, base *url.URL, c Case, result *CaseResult) error {
	c, err := expandCase(c, target.Bindings)
	if err != nil {
		return err
	}
	fixtureURL, err := resolveFixtureURL(base, c.Path)
	if err != nil {
		return err
	}
	wsURL := *fixtureURL
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	headers := http.Header{}
	if target.Credentials != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, wsURL.String(), nil)
		if err != nil {
			return errors.New("build websocket request")
		}
		if err := target.Credentials.Apply(req); err != nil {
			return errors.New("apply websocket credentials")
		}
		headers = req.Header
	}
	for key, value := range c.Headers {
		headers.Set(key, value)
	}

	conn, response, err := websocket.DefaultDialer.DialContext(ctx, wsURL.String(), headers)
	// The handshake response is returned on both the success and failure paths
	// and carries an open body that the dialer never closes for us.
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		if c.Exception != "" && response != nil && matchesException(c.Exception, response.StatusCode) {
			result.Status = response.StatusCode
			return nil
		}
		return errors.New("dial websocket")
	}
	defer conn.Close()
	for _, want := range c.WantWebSocketJSON {
		_, got, err := conn.ReadMessage()
		if err != nil {
			return errors.New("read websocket message")
		}
		if !sameJSON(want, got) {
			return errors.New("websocket JSON does not match fixture")
		}
	}
	if c.WantWebSocketFiltered {
		// Watch a bounded window. Legitimate traffic — keepalives, ordinary
		// events, or silence — is all acceptable; only a frame carrying an
		// excluded identifier fails.
		_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				// Deadline or close ends the watch; both mean no forbidden
				// frame arrived in the window.
				break
			}
			for _, forbidden := range c.AbsentStrings {
				if forbidden != "" && bytes.Contains(frame, []byte(forbidden)) {
					return errors.New("websocket frame contains an excluded fixture identifier")
				}
			}
		}
	}
	result.Status = http.StatusSwitchingProtocols
	return nil
}

// checkTimingDistribution interleaves protected and control requests, insists
// every sample answers the same negative status, and compares medians with a
// relative tolerance plus a small absolute floor. c must already be expanded.
func checkTimingDistribution(ctx context.Context, target Target, base *url.URL, raw Case) error {
	c, err := expandCase(raw, target.Bindings)
	if err != nil {
		return err
	}
	if c.Timing.ControlPath == "" {
		return errors.New("timing fixture has no control path")
	}
	samples := c.Timing.Samples
	if samples < 2 {
		samples = 2
	}
	maxRatio := c.Timing.MaxRatio
	if maxRatio <= 1 {
		maxRatio = 3
	}

	protected := make([]time.Duration, 0, samples)
	control := make([]time.Duration, 0, samples)
	expectedStatus := 0
	sample := func(path string, into *[]time.Duration) error {
		duration, status, err := timeSample(ctx, target, base, c, path)
		if err != nil {
			return err
		}
		// Every sample must satisfy the case's own negative expectation and
		// agree with every other sample's status: a control that answers a
		// visibly different status is an oracle regardless of elapsed time.
		if err := checkTimingSampleStatus(c, status); err != nil {
			return err
		}
		if expectedStatus == 0 {
			expectedStatus = status
		} else if status != expectedStatus {
			return fmt.Errorf("timing samples answer status %d and %d; protected and control responses must be indistinguishable", expectedStatus, status)
		}
		*into = append(*into, duration)
		return nil
	}
	for range samples {
		if err := sample(c.Path, &protected); err != nil {
			return err
		}
		if err := sample(c.Timing.ControlPath, &control); err != nil {
			return err
		}
	}

	medianProtected := timingMedian(protected)
	medianControl := timingMedian(control)
	smaller, larger := medianProtected, medianControl
	if smaller > larger {
		smaller, larger = larger, smaller
	}
	const noiseFloor = 2 * time.Millisecond
	if larger-smaller > noiseFloor && float64(larger) > float64(smaller)*maxRatio {
		return errors.New("protected and random missing-ID timing distributions diverge")
	}
	return nil
}

func checkTimingSampleStatus(c Case, status int) error {
	if c.Exception != "" {
		if !matchesException(c.Exception, status) {
			return fmt.Errorf("timing sample status %d does not match %s exception", status, c.Exception)
		}
		return nil
	}
	if c.WantStatus != 0 && status != c.WantStatus {
		return fmt.Errorf("timing sample status %d, want %d", status, c.WantStatus)
	}
	return nil
}

// timeSample issues one request with the case's own method, headers, and
// credentials — the identity a real client would time — and returns the
// elapsed time and status.
func timeSample(ctx context.Context, target Target, base *url.URL, c Case, path string) (time.Duration, int, error) {
	method := c.Method
	if method == "" {
		method = http.MethodGet
	}
	fixtureURL, err := resolveFixtureURL(base, path)
	if err != nil {
		return 0, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, fixtureURL.String(), nil)
	if err != nil {
		return 0, 0, errors.New("build timing request")
	}
	if target.Credentials != nil {
		if err := target.Credentials.Apply(req); err != nil {
			return 0, 0, errors.New("apply timing credentials")
		}
	}
	for key, value := range c.Headers {
		req.Header.Set(key, value)
	}
	started := time.Now()
	resp, err := target.Client.Do(req)
	if err != nil {
		return 0, 0, errors.New("send timing request")
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return time.Since(started), resp.StatusCode, nil
}

func resolveFixtureURL(base *url.URL, rawPath string) (*url.URL, error) {
	fixtureURL, err := url.Parse(rawPath)
	if err != nil || fixtureURL.IsAbs() || fixtureURL.Host != "" || hasSensitiveQuery(fixtureURL.Query()) {
		return nil, errors.New("compatibility contract fixture path is invalid or sensitive")
	}
	return base.ResolveReference(fixtureURL), nil
}

func timingMedian(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	middle := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[middle-1] + sorted[middle]) / 2
	}
	return sorted[middle]
}

func redactError(err error) string {
	message := err.Error()
	for _, key := range []string{"token", "secret", "password", "authorization", "cookie"} {
		if strings.Contains(strings.ToLower(message), key) {
			return "compatibility contract request failed"
		}
	}
	return message
}
