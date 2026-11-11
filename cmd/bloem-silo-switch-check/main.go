// Command bloem-silo-switch-check drives one step of the Silo ⇄ Bloem backend
// switching check against a running server. scripts/bloem/silo-switch-check.sh
// runs the steps in order, switching the backend between them, against one
// database: whatever one backend wrote, the other must read and extend.
//
// Usage: bloem-silo-switch-check <step> --base http://127.0.0.1:18090 --state state.json
// Steps: silo-setup, bloem-writes, silo-verify-and-write, bloem-verify.
//
// It speaks Silo's /api/v2 to both servers: Silo retired its v1 API, and the
// check must keep working against a Silo image that no longer serves it.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

type state struct {
	AdminPassword string `json:"admin_password"`
	UserPassword  string `json:"user_password"`
	U1            int    `json:"u1"`
	U2            int    `json:"u2"`
	U3            int    `json:"u3"`
	ExtraProfile  string `json:"extra_profile"`
}

type client struct {
	base  string
	token string
}

type adminUser struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	MaxStreams       *int   `json:"max_streams"`
	TranscodeAllowed *bool  `json:"transcode_allowed"`
	RequestsAllowed  *bool  `json:"requests_allowed"`
}

type profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

const adminName = "sw-admin"

var deviceHeaders = map[string]string{"X-Silo-Device-Id": "switch-check", "X-Silo-Device-Platform": "web"}

func main() {
	if len(os.Args) < 2 {
		fail("usage", errors.New("bloem-silo-switch-check <step> --base URL --state FILE"))
	}
	step := os.Args[1]
	flags := flag.NewFlagSet(step, flag.ExitOnError)
	base := flags.String("base", "", "server base URL")
	statePath := flags.String("state", "", "state file shared between steps")
	_ = flags.Parse(os.Args[2:])
	if *base == "" || *statePath == "" {
		fail("usage", errors.New("--base and --state are required"))
	}
	st := loadState(*statePath)
	c := &client{base: *base}
	switch step {
	case "silo-setup":
		siloSetup(c, &st)
	case "bloem-writes":
		bloemWrites(c, &st)
	case "silo-verify-and-write":
		siloVerifyAndWrite(c, &st)
	case "bloem-verify":
		bloemVerify(c, &st)
	default:
		fail("usage", fmt.Errorf("unknown step %q", step))
	}
	saveState(*statePath, st)
	fmt.Printf("PASS %s\n", step)
}

func siloSetup(c *client, st *state) {
	st.AdminPassword = randomPassword()
	st.UserPassword = randomPassword()
	must("setup admin", c.call(http.MethodPost, "/api/v2/auth/setup", map[string]any{
		"username": adminName, "email": adminName + "@example.invalid", "password": st.AdminPassword,
		"create_default_profile": true, "default_profile_name": "Main",
	}, nil, http.StatusOK, http.StatusCreated))
	c.login(adminName, st.AdminPassword, true)
	st.U1 = c.createUser("sw-u1", st.UserPassword, map[string]any{"max_streams": 3, "transcode_allowed": true})
}

func bloemWrites(c *client, st *state) {
	c.login(adminName, st.AdminPassword, true)
	u1 := c.getUser(st.U1)
	expectInt("u1 max_streams written by Silo", u1.MaxStreams, 3)
	must("update u1 on Bloem", c.updateUser(st.U1, map[string]any{
		"max_streams": 5, "transcode_allowed": false, "requests_allowed": false,
	}))
	u1 = c.getUser(st.U1)
	expectInt("u1 max_streams after Bloem update", u1.MaxStreams, 5)
	st.U2 = c.createUser("sw-u2", st.UserPassword, nil)
	var created profile
	must("create extra admin profile on Bloem", c.call(http.MethodPost, "/api/v2/profiles", map[string]any{"name": "Extra"}, &created, http.StatusOK, http.StatusCreated))
	if created.ID == "" {
		fail("create extra admin profile on Bloem", errors.New("response has no profile id"))
	}
	st.ExtraProfile = created.ID
}

func siloVerifyAndWrite(c *client, st *state) {
	c.login(adminName, st.AdminPassword, true)
	u1 := c.getUser(st.U1)
	expectInt("u1 max_streams written by Bloem", u1.MaxStreams, 5)
	expectBool("u1 transcode_allowed written by Bloem", u1.TranscodeAllowed, false)
	expectBool("u1 requests_allowed written by Bloem", u1.RequestsAllowed, false)
	c.getUser(st.U2)
	(&client{base: c.base}).login("sw-u2", st.UserPassword, false)
	expectProfiles(c, 2, "admin profiles after Bloem added one")

	must("update u1 on Silo", c.updateUser(st.U1, map[string]any{"max_streams": 8}))
	st.U3 = c.createUser("sw-u3", st.UserPassword, nil)
	must("delete extra admin profile on Silo", c.call(http.MethodDelete, "/api/v2/profiles/"+st.ExtraProfile, nil, nil, http.StatusOK, http.StatusNoContent))
	// u2 was created on Bloem: deleting it on Silo must cascade cleanly through
	// Bloem's membership and profile rows.
	must("delete u2 on Silo", c.writeUser(http.MethodDelete, st.U2, nil))
}

func bloemVerify(c *client, st *state) {
	c.login(adminName, st.AdminPassword, true)
	u1 := c.getUser(st.U1)
	expectInt("u1 max_streams written by Silo", u1.MaxStreams, 8)
	c.getUser(st.U3)
	u3 := &client{base: c.base}
	u3.login("sw-u3", st.UserPassword, true)
	expectProfiles(u3, 1, "profiles of the account Silo created")
	expectProfiles(c, 1, "admin profiles after Silo deleted one")
	if err := c.call(http.MethodGet, userPath(st.U2), nil, nil, http.StatusNotFound); err != nil {
		fail("u2 deleted on Silo is gone on Bloem", err)
	}
}

func (c *client) login(username, password string, withDevice bool) {
	var resp struct {
		AccessToken string `json:"access_token"`
	}
	headers := map[string]string{}
	if withDevice {
		headers = deviceHeaders
	}
	label := "login " + username
	if !withDevice {
		label += " without device headers"
	}
	must(label, c.request(http.MethodPost, "/api/v2/auth/login", map[string]any{"username": username, "password": password}, &resp, headers, nil, http.StatusOK))
	if resp.AccessToken == "" {
		fail(label, errors.New("no access token"))
	}
	c.token = resp.AccessToken
}

func (c *client) createUser(username, password string, extra map[string]any) int {
	body := map[string]any{
		"username": username, "email": username + "@example.invalid", "password": password, "role": "user",
		"create_default_profile": true, "default_profile_name": "Main", "library_ids": []string{},
	}
	for k, v := range extra {
		body[k] = v
	}
	var created adminUser
	must("create "+username, c.call(http.MethodPost, "/api/v2/admin/users", body, &created, http.StatusOK, http.StatusCreated))
	id, err := strconv.Atoi(created.ID)
	if err != nil || id <= 0 {
		fail("create "+username, fmt.Errorf("response has no user id: %q", created.ID))
	}
	return id
}

func userPath(id int) string { return fmt.Sprintf("/api/v2/admin/users/%d", id) }

func (c *client) getUser(id int) adminUser {
	var user adminUser
	must(fmt.Sprintf("get user %d", id), c.call(http.MethodGet, userPath(id), nil, &user, http.StatusOK))
	return user
}

// updateUser changes one account. /api/v2 takes writes only with If-Match
// carrying the account's current ETag.
func (c *client) updateUser(id int, fields map[string]any) error {
	return c.writeUser(http.MethodPut, id, fields)
}

func (c *client) writeUser(method string, id int, body any) error {
	var header http.Header
	if err := c.request(http.MethodGet, userPath(id), nil, nil, deviceHeaders, &header, http.StatusOK); err != nil {
		return err
	}
	etag := header.Get("ETag")
	if etag == "" {
		return fmt.Errorf("GET %s: no ETag", userPath(id))
	}
	headers := map[string]string{"If-Match": etag}
	for k, v := range deviceHeaders {
		headers[k] = v
	}
	return c.request(method, userPath(id), body, nil, headers, nil, http.StatusNoContent)
}

func expectProfiles(c *client, want int, label string) {
	var list struct {
		Items []profile `json:"items"`
	}
	must(label, c.call(http.MethodGet, "/api/v2/profiles", nil, &list, http.StatusOK))
	if len(list.Items) != want {
		fail(label, fmt.Errorf("got %d profiles, want %d", len(list.Items), want))
	}
}

func (c *client) call(method, path string, body, out any, want ...int) error {
	return c.request(method, path, body, out, deviceHeaders, nil, want...)
}

func (c *client) request(method, path string, body, out any, headers map[string]string, respHeader *http.Header, want ...int) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if respHeader != nil {
		*respHeader = resp.Header
	}
	ok := false
	for _, status := range want {
		ok = ok || resp.StatusCode == status
	}
	if !ok {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, truncate(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

func expectInt(label string, got *int, want int) {
	if got == nil || *got != want {
		fail(label, fmt.Errorf("got %v, want %d", deref(got), want))
	}
}

func expectBool(label string, got *bool, want bool) {
	if got == nil || *got != want {
		fail(label, fmt.Errorf("got %v, want %t", deref(got), want))
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func must(label string, err error) {
	if err != nil {
		fail(label, err)
	}
}

func fail(label string, err error) {
	fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", label, err)
	os.Exit(1)
}

func truncate(raw []byte) string {
	if len(raw) > 300 {
		raw = raw[:300]
	}
	return string(raw)
}

func randomPassword() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		fail("password", err)
	}
	return "Sw-" + hex.EncodeToString(b)
}

func loadState(path string) state {
	var st state
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st
	}
	if err != nil {
		fail("read state", err)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		fail("decode state", err)
	}
	return st
}

func saveState(path string, st state) {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		fail("encode state", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		fail("write state", err)
	}
}
