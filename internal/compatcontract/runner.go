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
		if len(c.WantWebSocketJSON) > 0 || c.WantWebSocketNoMessage {
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

func runHTTPCase(ctx context.Context, target Target, base *url.URL, c Case, result *CaseResult) error {
	method := c.Method
	if method == "" {
		method = http.MethodGet
	}
	caseURL, err := resolveFixtureURL(base, c.Path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, caseURL.String(), bytes.NewReader(c.Body))
	if err != nil {
		return errors.New("build request")
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
	if err := checkCaseResponse(c, resp, body); err != nil {
		return err
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

func sameJSON(want, got []byte) bool {
	var wantValue any
	var gotValue any
	return json.Unmarshal(want, &wantValue) == nil && json.Unmarshal(got, &gotValue) == nil && reflect.DeepEqual(wantValue, gotValue)
}

func runWebSocketCase(ctx context.Context, target Target, base *url.URL, c Case, result *CaseResult) error {
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
	if c.WantWebSocketNoMessage {
		_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		_, message, err := conn.ReadMessage()
		if err == nil {
			for _, forbidden := range c.AbsentStrings {
				if forbidden != "" && bytes.Contains(message, []byte(forbidden)) {
					return errors.New("websocket frame contains an excluded fixture identifier")
				}
			}
			return errors.New("websocket emitted an unexpected event frame")
		}
	}
	result.Status = http.StatusSwitchingProtocols
	return nil
}

func checkTimingDistribution(ctx context.Context, target Target, base *url.URL, c Case) error {
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
	protected, err := samplePath(ctx, target, base, c.Method, c.Path, samples)
	if err != nil {
		return err
	}
	control, err := samplePath(ctx, target, base, c.Method, c.Timing.ControlPath, samples)
	if err != nil {
		return err
	}
	if timingMean(protected) > time.Duration(float64(timingMean(control))*maxRatio)+20*time.Millisecond || timingMean(control) > time.Duration(float64(timingMean(protected))*maxRatio)+20*time.Millisecond {
		return errors.New("protected and random missing-ID timing distributions diverge")
	}
	return nil
}

func samplePath(ctx context.Context, target Target, base *url.URL, method, path string, samples int) ([]time.Duration, error) {
	if method == "" {
		method = http.MethodGet
	}
	durations := make([]time.Duration, 0, samples)
	for range samples {
		fixtureURL, err := resolveFixtureURL(base, path)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, fixtureURL.String(), nil)
		if err != nil {
			return nil, errors.New("build timing request")
		}
		if target.Credentials != nil {
			if err := target.Credentials.Apply(req); err != nil {
				return nil, errors.New("apply timing credentials")
			}
		}
		started := time.Now()
		resp, err := target.Client.Do(req)
		if err != nil {
			return nil, errors.New("send timing request")
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		durations = append(durations, time.Since(started))
	}
	return durations, nil
}

func resolveFixtureURL(base *url.URL, rawPath string) (*url.URL, error) {
	fixtureURL, err := url.Parse(rawPath)
	if err != nil || fixtureURL.IsAbs() || fixtureURL.Host != "" || hasSensitiveQuery(fixtureURL.Query()) {
		return nil, errors.New("compatibility contract fixture path is invalid or sensitive")
	}
	return base.ResolveReference(fixtureURL), nil
}

func timingMean(values []time.Duration) time.Duration {
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total / time.Duration(len(values))
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
