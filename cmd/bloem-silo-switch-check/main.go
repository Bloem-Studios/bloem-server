// Command bloem-silo-switch-check drives one step of the Silo ⇄ Bloem backend
// switching check against a running server. scripts/bloem/silo-switch-check.sh
// runs the steps in order, switching the backend between them, against one
// database: whatever one backend wrote, the other must read and extend.
//
// Usage: bloem-silo-switch-check <step> --base http://127.0.0.1:18090 --state state.json
// Steps: silo-setup, bloem-writes, silo-verify-and-write, bloem-verify.
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
	ID               int    `json:"id"`
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
	must("setup admin", c.call(http.MethodPost, "/api/v1/auth/setup", map[string]any{
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
	must("update u1 on Bloem", c.call(http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", st.U1), map[string]any{
		"max_streams": 5, "transcode_allowed": false, "requests_allowed": false,
	}, nil, http.StatusOK))
	u1 = c.getUser(st.U1)
	expectInt("u1 max_streams after Bloem update", u1.MaxStreams, 5)
	st.U2 = c.createUser("sw-u2", st.UserPassword, nil)
	var created profile
	must("create extra admin profile on Bloem", c.call(http.MethodPost, "/api/v1/profiles/", map[string]any{"name": "Extra"}, &created, http.StatusOK, http.StatusCreated))
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

	must("update u1 on Silo", c.call(http.MethodPut, fmt.Sprintf("/api/v1/admin/users/%d", st.U1), map[string]any{"max_streams": 8}, nil, http.StatusOK))
	st.U3 = c.createUser("sw-u3", st.UserPassword, nil)
	must("delete extra admin profile on Silo", c.call(http.MethodDelete, "/api/v1/profiles/"+st.ExtraProfile, nil, nil, http.StatusOK, http.StatusNoContent))
	// u2 was created on Bloem: deleting it on Silo must cascade cleanly through
	// Bloem's membership and profile rows.
	must("delete u2 on Silo", c.call(http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", st.U2), nil, nil, http.StatusOK, http.StatusNoContent))
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
	if err := c.call(http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d", st.U2), nil, nil, http.StatusNotFound); err != nil {
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
	must(label, c.request(http.MethodPost, "/api/v1/auth/login", map[string]any{"username": username, "password": password}, &resp, headers, http.StatusOK))
	if resp.AccessToken == "" {
		fail(label, errors.New("no access token"))
	}
	c.token = resp.AccessToken
}

func (c *client) createUser(username, password string, extra map[string]any) int {
	body := map[string]any{
		"username": username, "email": username + "@example.invalid", "password": password, "role": "user",
		"create_default_profile": true, "default_profile_name": "Main", "library_ids": []int{},
	}
	for k, v := range extra {
		body[k] = v
	}
	var created adminUser
	must("create "+username, c.call(http.MethodPost, "/api/v1/admin/users", body, &created, http.StatusOK, http.StatusCreated))
	if created.ID == 0 {
		fail("create "+username, errors.New("response has no user id"))
	}
	return created.ID
}

func (c *client) getUser(id int) adminUser {
	var user adminUser
	must(fmt.Sprintf("get user %d", id), c.call(http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d", id), nil, &user, http.StatusOK))
	return user
}

func expectProfiles(c *client, want int, label string) {
	var list struct {
		Profiles []profile `json:"profiles"`
	}
	must(label, c.call(http.MethodGet, "/api/v1/profiles/", nil, &list, http.StatusOK))
	if len(list.Profiles) != want {
		fail(label, fmt.Errorf("got %d profiles, want %d", len(list.Profiles), want))
	}
}

func (c *client) call(method, path string, body, out any, want ...int) error {
	return c.request(method, path, body, out, deviceHeaders, want...)
}

func (c *client) request(method, path string, body, out any, headers map[string]string, want ...int) error {
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
