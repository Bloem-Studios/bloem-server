package api

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/taskmanager/tasks"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

// The audiobook request lifecycle acceptance drives the real plugin
// executable (BLOEM_AUDIOBOOK_REQUEST_PLUGIN_BINARY) through the production
// acquisition adapters (composeBloemBookFulfillment), the platform-admin
// setup, the encrypted request integration, the scheduled reconcile task, the
// real scanner and PostgreSQL. Release sources and download clients are
// in-process fakes; the downloads are real audio files generated with ffmpeg
// and probed by the scanner.
//
// Routers resolve from the test's own plugin host, not through plugins.Service
// (PluginRequestRouterAdapter): the installation, installed-manifest and
// launch checks of that path are not covered here.
//
// Requirements: SILO_TEST_DATABASE_URL, the plugin binary, ffmpeg and
// ffprobe. Without the plugin binary the tests skip; once it is set, a missing
// ffmpeg or ffprobe fails them.

const (
	lifecycleTitle      = "Synthetic Saga"
	lifecycleAuthor     = "Test Author"
	lifecycleQBPassword = "qb-lifecycle-secret"
	lifecycleNZBSecret  = "nzb-lifecycle-secret"
	lifecycleQBRoot     = "/data/bloem-requests"
	lifecycleNZBRoot    = "/nzb/complete"
	lifecycleCategory   = "bloem-audiobooks"
)

type lifecycleTools struct{ binary, ffmpeg, ffprobe string }

func lifecyclePrerequisites(t *testing.T) lifecycleTools {
	t.Helper()
	binary := os.Getenv("BLOEM_AUDIOBOOK_REQUEST_PLUGIN_BINARY")
	if binary == "" {
		t.Skip("BLOEM_AUDIOBOOK_REQUEST_PLUGIN_BINARY not set")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("ffmpeg unavailable; the lifecycle acceptance generates real audio")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal("ffprobe unavailable; the scanner probes the downloaded audio")
	}
	return lifecycleTools{binary: binary, ffmpeg: ffmpeg, ffprobe: ffprobe}
}

// lifecycleAudio writes seconds of a sine tone. Faststart puts the MP4 movie
// header first, where the plugin reads durations; MP3 gets an Info frame.
func lifecycleAudio(t *testing.T, ffmpeg, file string, seconds int, bitrate string) int64 {
	t.Helper()
	return lifecycleAudioLayout(t, ffmpeg, file, seconds, bitrate, true)
}

// lifecycleAudioLayout writes the M4B movie header first (faststart) or, as
// ffmpeg does by default, after the audio.
func lifecycleAudioLayout(t *testing.T, ffmpeg, file string, seconds int, bitrate string, faststart bool) int64 {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=44100:duration=%d", seconds), "-ac", "1",
		"-metadata", "title=" + lifecycleTitle, "-metadata", "artist=" + lifecycleAuthor, "-metadata", "album=" + lifecycleTitle}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".m4b":
		args = append(args, "-c:a", "aac", "-b:a", bitrate)
		if faststart {
			args = append(args, "-movflags", "+faststart")
		}
		args = append(args, "-f", "mp4", file)
	case ".mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", bitrate, "-write_xing", "1", file)
	default:
		t.Fatalf("unsupported fixture %s", file)
	}
	if out, err := exec.CommandContext(t.Context(), ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("generate %s: %v: %s", file, err, out)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func lifecycleMB(size int64) string { return strconv.FormatFloat(float64(size)/(1<<20), 'f', 2, 64) }

// ---------------------------------------------------------------- sources

type lifecycleABBPost struct {
	Slug, Title, Language, Format, SizeMB, Hash string
}

type lifecycleNZBRow struct {
	ID, Subject, Parts, Size string
	Password                 bool
}

// lifecycleSources serves AudiobookBay search/detail pages and NZBKing search
// pages over TLS; the plugin requires HTTPS source origins.
type lifecycleSources struct {
	mu       sync.Mutex
	server   *httptest.Server
	posts    []lifecycleABBPost
	rows     []lifecycleNZBRow
	searches []string
	details  []string
}

func newLifecycleSources(t *testing.T) *lifecycleSources {
	t.Helper()
	s := &lifecycleSources{}
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.server.Close)
	return s
}

func (s *lifecycleSources) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/page/1/":
		s.searches = append(s.searches, "abb:"+r.URL.Query().Get("s"))
		var b strings.Builder
		b.WriteString(`<html><body><div id="content">`)
		for _, p := range s.posts {
			fmt.Fprintf(&b, `<div class="post"><div class="postTitle"><h2><a href="/abss/%s/" rel="bookmark">%s</a></h2></div>`+
				`<div class="postInfo">Category: General Fiction&nbsp; <br />Language: %s<span style="margin-left:100px;">Keywords: Fiction&nbsp </span><br /></div>`+
				`<div class="postContent"><p style='text-align:center;'>Posted: 01 Oct 2026<br />Format: <span>%s</span> / Bitrate: <span>64 Kbps</span><br />File Size: <span>%s</span> MBs</p></div></div>`,
				p.Slug, p.Title, p.Language, p.Format, p.SizeMB)
		}
		b.WriteString(`</div></body></html>`)
		_, _ = io.WriteString(w, b.String())
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/page/"):
		_, _ = io.WriteString(w, "<html><body>Not Found</body></html>")
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/abss/"):
		s.details = append(s.details, r.URL.Path)
		for _, p := range s.posts {
			if r.URL.Path == "/abss/"+p.Slug+"/" {
				_, _ = fmt.Fprintf(w, `<html><body><h1>%s</h1><table><tr><td>Info Hash:</td><td>%s</td></tr>`+
					`<tr><td>Trackers:</td><td>udp://tracker.example.org:6969/announce</td></tr></table></body></html>`, p.Title, p.Hash)
				return
			}
		}
		http.NotFound(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/search/":
		s.searches = append(s.searches, "nzbking:"+r.URL.Query().Get("q"))
		var b strings.Builder
		b.WriteString(`<html><body><div class='search-results'>`)
		for _, row := range s.rows {
			password := "(NO PASSWORD)"
			if row.Password {
				password = "PASSWORD PROTECTED"
			}
			fmt.Fprintf(&b, `<div class='search-result'><div class='search-select'><input type="checkbox" name="nzb" value="%s"></div>`+
				`<div class='search-subject'>%s <br><a href="/nzb:%s/" class="button" download>NZB</a> <a href="/details:%s/" class="button">Details</a>`+
				` parts: <span class="allgood">%s</span> <span>%s</span> size: %s filetypes: .MP3 .NZB</div>`+
				`<div class='search-groups'>a.b.audiobooks</div><div class='search-age'>3d</div></div>`,
				row.ID, row.Subject, row.ID, row.ID, row.Parts, password, row.Size)
		}
		b.WriteString(`</div>Query time: 0.01s</body></html>`)
		_, _ = io.WriteString(w, b.String())
	default:
		http.NotFound(w, r)
	}
}

// setPosts replaces the AudiobookBay listing between hand-offs.
func (s *lifecycleSources) setPosts(posts ...lifecycleABBPost) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts = posts
}

func (s *lifecycleSources) seen() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.searches), slices.Clone(s.details)
}

// trustSources makes the plugin process, which inherits the test
// environment, trust the TLS fixture.
func (s *lifecycleSources) trustSources(t *testing.T) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sources.pem")
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", file)
	t.Setenv("SSL_CERT_DIR", t.TempDir())
}

// ---------------------------------------------------------------- qBittorrent

type lifecycleTorrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Progress     float64 `json:"progress"`
	Category     string  `json:"category"`
	Tags         string  `json:"tags"`
	Size         int64   `json:"size"`
	LastActivity int64   `json:"last_activity"`
	AddedOn      int64   `json:"added_on"`
	SavePath     string  `json:"save_path"`
	files        []lifecycleTorrentFile
}

type lifecycleTorrentFile struct {
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
	Priority int     `json:"priority"`
}

type lifecycleQBAdd struct{ Magnet, Hash, Category, Tag, SavePath string }

// lifecycleQBittorrent is a stateful Web API v2 fake with a session cookie.
// Everything the plugin must never call (delete, pause, recheck, setLocation,
// preferences) lands in forbidden.
type lifecycleQBittorrent struct {
	mu        sync.Mutex
	server    *httptest.Server
	localRoot string
	torrents  []*lifecycleTorrent
	adds      []lifecycleQBAdd
	logins    int
	forbidden []string
	// addFails makes the next add answer 503 without recording it.
	addFails bool
	// down answers 503 to every call, the login included.
	down bool
}

func newLifecycleQBittorrent(t *testing.T, localRoot string) *lifecycleQBittorrent {
	t.Helper()
	q := &lifecycleQBittorrent{localRoot: localRoot}
	q.server = httptest.NewServer(http.HandlerFunc(q.serve))
	t.Cleanup(q.server.Close)
	return q
}

func (q *lifecycleQBittorrent) serve(w http.ResponseWriter, r *http.Request) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/api/v2/auth/login" {
		if r.Method != http.MethodPost || r.ParseForm() != nil || r.PostForm.Get("username") != "bloem" || r.PostForm.Get("password") != lifecycleQBPassword {
			_, _ = io.WriteString(w, "Fails.")
			return
		}
		q.logins++
		http.SetCookie(w, &http.Cookie{Name: "SID", Value: "lifecycle-session", Path: "/"})
		_, _ = io.WriteString(w, "Ok.")
		return
	}
	if c, err := r.Cookie("SID"); err != nil || c.Value != "lifecycle-session" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/app/webapiVersion":
		_, _ = io.WriteString(w, "2.11.0")
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/torrents/categories":
		_ = json.NewEncoder(w).Encode(map[string]any{lifecycleCategory: map[string]string{"name": lifecycleCategory, "savePath": ""}, "tv-sonarr": map[string]string{"name": "tv-sonarr"}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/torrents/info":
		query := r.URL.Query()
		out := []*lifecycleTorrent{}
		for _, torrent := range q.torrents {
			if hashes := query.Get("hashes"); hashes != "" && !strings.EqualFold(hashes, torrent.Hash) {
				continue
			}
			if tag := query.Get("tag"); tag != "" && !slices.Contains(strings.Split(torrent.Tags, ","), tag) {
				continue
			}
			out = append(out, torrent)
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/torrents/files":
		for _, torrent := range q.torrents {
			if strings.EqualFold(torrent.Hash, r.URL.Query().Get("hash")) {
				_ = json.NewEncoder(w).Encode(torrent.files)
				return
			}
		}
		http.NotFound(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/torrents/add":
		add, err := lifecycleParseAdd(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if q.addFails {
			q.addFails = false
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		q.adds = append(q.adds, add)
		q.torrents = append(q.torrents, &lifecycleTorrent{Hash: add.Hash, Name: lifecycleTitle, State: "metaDL", Category: add.Category, Tags: add.Tag, SavePath: add.SavePath, AddedOn: time.Now().Unix()})
		_, _ = io.WriteString(w, "Ok.")
	default:
		q.forbidden = append(q.forbidden, r.Method+" "+r.URL.Path)
		http.Error(w, "unexpected operation", http.StatusInternalServerError)
	}
}

func lifecycleParseAdd(r *http.Request) (lifecycleQBAdd, error) {
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		return lifecycleQBAdd{}, errors.New("add must be multipart")
	}
	form, err := multipart.NewReader(r.Body, params["boundary"]).ReadForm(1 << 20)
	if err != nil {
		return lifecycleQBAdd{}, err
	}
	field := func(name string) string {
		if v := form.Value[name]; len(v) == 1 {
			return v[0]
		}
		return ""
	}
	if field("autoTMM") != "false" || field("paused") != "false" {
		return lifecycleQBAdd{}, errors.New("add must pin its save path and start")
	}
	add := lifecycleQBAdd{Magnet: field("urls"), Category: field("category"), Tag: field("tags"), SavePath: field("savepath")}
	values, err := url.ParseQuery(strings.TrimPrefix(add.Magnet, "magnet:?"))
	if err != nil || !strings.HasPrefix(add.Magnet, "magnet:?") {
		return lifecycleQBAdd{}, errors.New("add needs a magnet")
	}
	add.Hash = strings.TrimPrefix(values.Get("xt"), "urn:btih:")
	return add, nil
}

func (q *lifecycleQBittorrent) torrent(t *testing.T, hash string) *lifecycleTorrent {
	t.Helper()
	for _, torrent := range q.torrents {
		if torrent.Hash == hash {
			return torrent
		}
	}
	t.Fatalf("torrent %s was never added", hash)
	return nil
}

func (q *lifecycleQBittorrent) setProgress(t *testing.T, hash, state string, progress float64) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	torrent := q.torrent(t, hash)
	torrent.State, torrent.Progress, torrent.LastActivity = state, progress, time.Now().Unix()
}

// finish lays the generated files down where qBittorrent's save path maps on
// this server and reports the torrent as seeding.
func (q *lifecycleQBittorrent) finish(t *testing.T, hash string, write func(dir string) []lifecycleTorrentFile) {
	t.Helper()
	q.mu.Lock()
	torrent := q.torrent(t, hash)
	save := torrent.SavePath
	q.mu.Unlock()
	rel, ok := strings.CutPrefix(save, lifecycleQBRoot+"/")
	if !ok {
		t.Fatalf("save path %q is outside the client root", save)
	}
	files := write(filepath.Join(q.localRoot, rel))
	q.mu.Lock()
	defer q.mu.Unlock()
	torrent.files, torrent.State, torrent.Progress, torrent.LastActivity = files, "uploading", 1, time.Now().Unix()
	for _, f := range files {
		torrent.Size += f.Size
	}
}

func (q *lifecycleQBittorrent) setDown(down bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.down = down
}

// lifecycleCopy copies staged files, keyed by their path inside the
// download, into dir and lists them as qBittorrent would.
func lifecycleCopy(t *testing.T, dir string, staged map[string]string) []lifecycleTorrentFile {
	t.Helper()
	names := slices.Sorted(maps.Keys(staged))
	files := make([]lifecycleTorrentFile, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(staged[name])
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, lifecycleTorrentFile{Name: name, Size: int64(len(data)), Progress: 1, Priority: 1})
	}
	return files
}

func (q *lifecycleQBittorrent) remove(t *testing.T, hash string) {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.torrents = slices.DeleteFunc(q.torrents, func(torrent *lifecycleTorrent) bool { return torrent.Hash == hash })
}

func (q *lifecycleQBittorrent) snapshot() ([]lifecycleQBAdd, []string, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.adds), slices.Clone(q.forbidden), q.logins
}

// ---------------------------------------------------------------- NZBGet

type lifecycleNZBItem struct {
	NZBID    int64  `json:"NZBID"`
	NZBName  string `json:"NZBName,omitempty"`
	Name     string `json:"Name,omitempty"`
	Status   string `json:"Status"`
	Kind     string `json:"Kind,omitempty"`
	DupeKey  string `json:"DupeKey"`
	Category string `json:"Category"`
	DestDir  string `json:"DestDir"`
	FinalDir string `json:"FinalDir"`
}

type lifecycleNZBAppend struct {
	ID                                     int64
	Name, URL, Category, DupeKey, DupeMode string
}

// lifecycleNZBGet is a JSON-RPC fake with Basic auth. Its config carries a
// password entry the plugin must discard while reading category names.
type lifecycleNZBGet struct {
	mu        sync.Mutex
	server    *httptest.Server
	localRoot string
	queue     []*lifecycleNZBItem
	history   []*lifecycleNZBItem
	appends   []lifecycleNZBAppend
	forbidden []string
	next      int64
}

func newLifecycleNZBGet(t *testing.T, localRoot string) *lifecycleNZBGet {
	t.Helper()
	n := &lifecycleNZBGet{localRoot: localRoot, next: 40}
	n.server = httptest.NewServer(http.HandlerFunc(n.serve))
	t.Cleanup(n.server.Close)
	return n
}

func (n *lifecycleNZBGet) serve(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	user, password, ok := r.BasicAuth()
	if r.URL.Path != "/jsonrpc" || r.Method != http.MethodPost || r.URL.RawQuery != "" {
		n.forbidden = append(n.forbidden, r.Method+" "+r.URL.String())
		http.NotFound(w, r)
		return
	}
	if !ok || user != "bloem" || password != lifecycleNZBSecret {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var call struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	reply := func(result any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "1.1", "id": 1, "result": result})
	}
	switch call.Method {
	case "version":
		reply("24.3")
	case "config":
		reply([]map[string]string{{"Name": "MainDir", "Value": "/nzb"}, {"Name": "ControlPassword", "Value": "must-not-leave-the-plugin"},
			{"Name": "Category1.Name", "Value": "movies"}, {"Name": "Category2.Name", "Value": lifecycleCategory}})
	case "listgroups":
		reply(append([]*lifecycleNZBItem{}, n.queue...))
	case "history":
		reply(append([]*lifecycleNZBItem{}, n.history...))
	case "append":
		var name, link, category, dupeKey, dupeMode string
		if len(call.Params) != 10 || json.Unmarshal(call.Params[0], &name) != nil || json.Unmarshal(call.Params[1], &link) != nil ||
			json.Unmarshal(call.Params[2], &category) != nil || json.Unmarshal(call.Params[6], &dupeKey) != nil || json.Unmarshal(call.Params[8], &dupeMode) != nil {
			reply(0)
			return
		}
		n.next++
		n.appends = append(n.appends, lifecycleNZBAppend{ID: n.next, Name: name, URL: link, Category: category, DupeKey: dupeKey, DupeMode: dupeMode})
		folder := strings.TrimSuffix(name, ".nzb")
		n.queue = append(n.queue, &lifecycleNZBItem{NZBID: n.next, NZBName: folder, Status: "QUEUED", DupeKey: dupeKey, Category: category, DestDir: "/nzb/intermediate/" + folder})
		reply(n.next)
	default:
		n.forbidden = append(n.forbidden, "rpc "+call.Method)
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "1.1", "id": 1, "error": map[string]any{"code": 1, "message": "unexpected"}})
	}
}

func (n *lifecycleNZBGet) item(t *testing.T, id int64) *lifecycleNZBItem {
	t.Helper()
	for _, it := range n.queue {
		if it.NZBID == id {
			return it
		}
	}
	t.Fatalf("NZB %d is not queued", id)
	return nil
}

func (n *lifecycleNZBGet) setStatus(t *testing.T, id int64, status string) {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.item(t, id).Status = status
}

// finish unpacks the generated files into the category's final folder and
// moves the entry to history, as NZBGet does after post-processing.
func (n *lifecycleNZBGet) finish(t *testing.T, id int64, write func(dir string)) {
	t.Helper()
	n.mu.Lock()
	it := n.item(t, id)
	folder := it.NZBName
	n.mu.Unlock()
	write(filepath.Join(n.localRoot, folder))
	n.mu.Lock()
	defer n.mu.Unlock()
	n.queue = slices.DeleteFunc(n.queue, func(q *lifecycleNZBItem) bool { return q.NZBID == id })
	n.history = append(n.history, &lifecycleNZBItem{NZBID: id, Name: folder, Status: "SUCCESS/ALL", Kind: "NZB", DupeKey: it.DupeKey, Category: it.Category,
		DestDir: it.DestDir, FinalDir: lifecycleNZBRoot + "/" + folder})
}

func (n *lifecycleNZBGet) snapshot() ([]lifecycleNZBAppend, []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.appends), slices.Clone(n.forbidden)
}

// ---------------------------------------------------------------- harness

type lifecycleNotices struct {
	mu        sync.Mutex
	lifecycle []string
	fulfilled []string
	attention []string
}

func (n *lifecycleNotices) Lifecycle(_ context.Context, r bookrequests.Request, event string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lifecycle = append(n.lifecycle, r.ID+":"+event)
}

func (n *lifecycleNotices) Fulfilled(_ context.Context, r bookrequests.Request) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fulfilled = append(n.fulfilled, r.ID)
	return nil
}

func (n *lifecycleNotices) Attention(_ context.Context, r bookrequests.Request, attempt int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.attention = append(n.attention, fmt.Sprintf("%s:%d", r.ID, attempt))
}

func (n *lifecycleNotices) snapshot() (lifecycle, fulfilled, attention []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.lifecycle), slices.Clone(n.fulfilled), slices.Clone(n.attention)
}

// lifecycleHostResolver resolves the router through the live plugin host on
// every call, as production does, so a restarted process is picked up.
type lifecycleHostResolver struct {
	host         *pluginhost.Host
	installation int
}

func (r lifecycleHostResolver) RequestRouterClient(_ context.Context, installationID int, capabilityID string) (requests.RouterClient, error) {
	if installationID != r.installation {
		return nil, errors.New("unknown plugin installation")
	}
	client, err := r.host.Client(installationID)
	if err != nil {
		return nil, err
	}
	router, err := client.RequestRouter(capabilityID)
	if err != nil {
		return nil, err
	}
	return router, nil
}

type lifecycleProgress struct{}

func (lifecycleProgress) Report(float64, string)        {}
func (lifecycleProgress) SetResultData(json.RawMessage) {}

type audiobookLifecycle struct {
	tools        lifecycleTools
	pool         *pgxpool.Pool
	books        *bookrequests.Service
	setup        bloemAudiobookSetup
	task         *tasks.BookRequestsTask
	notices      *lifecycleNotices
	host         *pluginhost.Host
	manifest     *pluginv1.PluginManifest
	installation int
	admin        bookrequests.Viewer
	reader       bookrequests.Viewer
	library      string
	downloads    string
	folder       *models.MediaFolder
	scanner      *scanner.Scanner
}

func newAudiobookLifecycle(t *testing.T, tools lifecycleTools) *audiobookLifecycle {
	t.Helper()
	pool := newDisposableAPIDatabase(t, "bloem_audiobook_lifecycle_", true)
	ctx := t.Context()
	if err := database.RunMigrations(ctx, pool, migrations.BloemFS, "sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatal(err)
	}
	sqlExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	l := &audiobookLifecycle{tools: tools, pool: pool, notices: &lifecycleNotices{}}
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&l.admin.OrganizationID); err != nil {
		t.Fatal(err)
	}
	l.reader.OrganizationID = l.admin.OrganizationID
	l.admin.Admin = true
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES('lifecycle-admin','lifecycle-admin@example.test','x','admin') RETURNING id`).Scan(&l.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,email,password_hash,role) VALUES('lifecycle-reader','lifecycle-reader@example.test','x','user') RETURNING id`).Scan(&l.reader.UserID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
		t.Fatal(err)
	}
	for account, role := range map[int]string{l.admin.UserID: "admin", l.reader.UserID: "user"} {
		if _, err = tx.Exec(ctx, `INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role) VALUES($1,$2,'active',$3) ON CONFLICT DO NOTHING`, l.admin.OrganizationID, account, role); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	l.reader.ProfileID = "reader"
	sqlExec(`INSERT INTO user_profiles(user_id,id,name,organization_id,access_group_id) SELECT $1,'reader','Reader',$2,g.id FROM access_groups g WHERE g.organization_id=$2 AND g.is_default`, l.reader.UserID, l.admin.OrganizationID)
	// The live posture: requests on, auto-approval on, five per seven days.
	sqlExec(`UPDATE request_settings SET requests_enabled=true,global_max_requests=5,global_window_days=7,global_auto_approval_enabled=true WHERE id=true`)

	// A platform-owned audiobook library entitled to the default organization,
	// with one existing book outside the dedicated download folder.
	base := t.TempDir()
	l.library = filepath.Join(base, "library")
	l.downloads = filepath.Join(l.library, "requests")
	if err = os.MkdirAll(l.downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	lifecycleAudio(t, tools.ffmpeg, filepath.Join(l.library, "Existing Author", "Existing Book", "Existing Book.mp3"), 5, "32k")
	var folderID int
	if err = pool.QueryRow(ctx, `INSERT INTO media_folders(name,type,owner_id) VALUES('Audio Books','audiobooks',bloem_platform_resource_owner_id()) RETURNING id`).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	sqlExec(`INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)`, folderID, l.library)
	sqlExec(`INSERT INTO organization_entitlements(organization_id,media_folder_id,root_owner_id,root_kind,entitlement_kind,status,granted_by_service)
 SELECT $1,$2,bloem_platform_resource_owner_id(),'media_folder','library_access','active','audiobook-lifecycle-test'
 WHERE NOT EXISTS(SELECT 1 FROM organization_entitlements WHERE organization_id=$1 AND media_folder_id=$2 AND status='active')`, l.admin.OrganizationID, folderID)
	l.folder = &models.MediaFolder{ID: folderID, Type: "audiobooks", Name: "Audio Books", Paths: []string{l.library}, Enabled: true}
	l.scanner = scanner.NewScanner(scanner.NewFileRepository(pool), tools.ffprobe, nil, 2, false, 0)

	// The installed plugin, as the offline installer records it.
	if err = pool.QueryRow(ctx, `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES($1,'0.1.0',$2,bloem_platform_resource_owner_id()) RETURNING id`,
		bookrequests.AudiobookRequestPluginID, filepath.Dir(tools.binary)).Scan(&l.installation); err != nil {
		t.Fatal(err)
	}
	sqlExec(`INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id) VALUES($1,'request_router.v1','audiobooks')`, l.installation)
	manifestJSON, err := exec.CommandContext(ctx, tools.binary, "manifest").Output()
	if err != nil {
		t.Fatalf("read plugin manifest: %v", err)
	}
	l.manifest = &pluginv1.PluginManifest{}
	if err = protojson.Unmarshal(manifestJSON, l.manifest); err != nil {
		t.Fatal(err)
	}
	if l.manifest.PluginId != bookrequests.AudiobookRequestPluginID {
		t.Fatalf("plugin binary is %q", l.manifest.PluginId)
	}
	l.host = pluginhost.NewHost(pluginhost.Config{Logger: hclog.NewNullLogger()})
	t.Cleanup(func() { _ = l.host.Shutdown(context.Background()) })

	// The production adapters (composeBloemBookFulfillment) and setup types
	// (newBloemBookRequests), with the plugin host as the router resolver.
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-lifecycle-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	repository := requests.NewRepository(pool, cipher)
	resolver := lifecycleHostResolver{host: l.host, installation: l.installation}
	connections := requests.NewService(repository, nil, nil)
	connections.SetTenantScopeResolver(tenancy.NewStore(pool))
	connections.SetRouterProvider(requests.NewPluginRouterProvider(resolver))
	l.books = bookrequests.NewService(pool, l.notices)
	composeBloemBookFulfillment(l.books, repository, resolver)
	l.setup = bloemAudiobookSetup{pool: pool, books: l.books, requests: connections, platform: auth.NewPlatformAdminAuthorizer(auth.NewUserRepository(pool))}
	l.task = tasks.NewBookRequestsTask(l.books)
	return l
}

func (l *audiobookLifecycle) startPlugin(t *testing.T) {
	t.Helper()
	if _, err := l.host.Start(t.Context(), pluginhost.StartRequest{InstallationID: l.installation, BinaryPath: l.tools.binary, Manifest: l.manifest}); err != nil {
		t.Fatalf("start plugin: %v", err)
	}
}

// connect creates the connection through the platform-admin setup (the
// plugin validates it) and binds the dedicated download folder.
func (l *audiobookLifecycle) connect(t *testing.T, config map[string]any, credentials map[string]string) string {
	t.Helper()
	summary, err := l.setup.Create(t.Context(), l.admin, handlers.AudiobookRequestConnectionInput{Name: "Audiobook acquisition", InstallationID: l.installation, CapabilityID: "audiobooks", PluginConfig: config, Credentials: credentials})
	if err != nil {
		var validation *requests.ValidationError
		if errors.As(err, &validation) {
			t.Fatalf("plugin rejected the connection: %+v", validation)
		}
		t.Fatalf("create connection: %v", err)
	}
	bound, err := l.setup.SetLibrary(t.Context(), l.admin, summary.ID, l.folder.ID, []string{l.downloads})
	if err != nil || !bound.Ready {
		t.Fatalf("bind library: %+v %v", bound, err)
	}
	var stored string
	if err = l.pool.QueryRow(t.Context(), `SELECT api_key_ref FROM request_integrations WHERE id=$1`, summary.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, value := range credentials {
		if strings.Contains(stored, value) {
			t.Fatal("download client credentials are stored in clear text")
		}
	}
	return summary.ID
}

func (l *audiobookLifecycle) file(t *testing.T, title string) bookrequests.Request {
	t.Helper()
	r, created, err := l.books.Create(t.Context(), l.reader, bookrequests.CreateInput{Title: title, Author: lifecycleAuthor}, "lifecycle-"+uuid.NewString())
	if err != nil || !created || r.MediaType != "audiobook" || r.Status != "approved" {
		t.Fatalf("file %q: %+v created=%v %v", title, r, created, err)
	}
	return r
}

// run executes one pass of the scheduled reconcile task. due skips the poll
// delay first, as waiting for the next five-minute pass would.
func (l *audiobookLifecycle) run(t *testing.T, id string, due bool) bookrequests.Request {
	t.Helper()
	if due {
		if _, err := l.pool.Exec(t.Context(), `UPDATE bloem_ebook_request_dispatches SET next_attempt_at=now()-interval '1 second' WHERE request_id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.task.Execute(t.Context(), lifecycleProgress{}); err != nil {
		t.Fatalf("reconcile task: %v", err)
	}
	r, err := l.books.Get(t.Context(), l.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (l *audiobookLifecycle) acquisition(t *testing.T, id string) bookrequests.AudiobookAcquisition {
	t.Helper()
	a, err := l.books.AudiobookAcquisition(t.Context(), l.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (l *audiobookLifecycle) scan(t *testing.T) {
	t.Helper()
	if err := l.scanner.ScanAudiobookFolder(t.Context(), l.folder, true); err != nil {
		t.Fatalf("scan library: %v", err)
	}
}

// scannedItem returns the one audiobook item with files in dir.
func (l *audiobookLifecycle) scannedItem(t *testing.T, dir string) (string, int) {
	t.Helper()
	var ids []string
	var files int
	rows, err := l.pool.Query(t.Context(), `SELECT DISTINCT f.content_id,count(*) OVER (PARTITION BY f.content_id) FROM media_files f JOIN media_items i ON i.content_id=f.content_id AND i.type='audiobook'
 WHERE f.media_folder_id=$1 AND f.missing_since IS NULL AND f.file_path LIKE $2`, l.folder.ID, dir+"/%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id, &files); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Fatalf("scanned items under %s: %v", dir, ids)
	}
	return ids[0], files
}

func (l *audiobookLifecycle) requireNoSecrets(t *testing.T, id string, secrets ...string) {
	t.Helper()
	var dump string
	if err := l.pool.QueryRow(t.Context(), `SELECT coalesce((SELECT string_agg(envelope::text,' ') FROM bloem_audiobook_request_decisions WHERE request_id=$1),'')||
 (SELECT d.external_id||d.descriptor::text FROM bloem_ebook_request_dispatches d WHERE d.request_id=$1)||(SELECT row_to_json(r)::text FROM bloem_book_requests r WHERE r.id=$1)`, id).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if strings.Contains(dump, s) {
			t.Fatalf("request %s state carries a credential", id)
		}
	}
}

func lifecycleDecision(t *testing.T, a bookrequests.AudiobookAcquisition, phase string) bookrequests.AcquisitionDecision {
	t.Helper()
	for _, record := range a.Decisions {
		if record.Decision.Phase == phase {
			return record.Decision
		}
	}
	t.Fatalf("no %s decision in %+v", phase, a.Decisions)
	return bookrequests.AcquisitionDecision{}
}

// lifecycleFulfillment reads the request's persisted fulfillment state.
func lifecycleFulfillment(r bookrequests.Request) string {
	return r.Fulfilment //nolint:misspell // persisted field and wire name
}

func lifecycleMarkerFolder(r bookrequests.Request, attempt int) string {
	return r.Title + " [" + bookrequests.AudiobookMarker(r.ID, attempt) + "]"
}

func lifecycleTorrentConfig(sources *lifecycleSources, qb *lifecycleQBittorrent, downloads string) map[string]any {
	return map[string]any{
		"audiobookbay_base_url": sources.server.URL, "audiobookbay_search_pages": 1, "nzbking_enabled": false,
		"qbittorrent_url": qb.server.URL, "qbittorrent_username": "bloem", "qbittorrent_category": lifecycleCategory, "qbittorrent_save_root": lifecycleQBRoot,
		"allow_http_download_clients": true, "allow_private_source_hosts": true, "allow_loopback_targets": true,
		"verify_min_total_mb": 1, "verify_min_duration_minutes": 5,
		"verify_local_client_prefix": lifecycleQBRoot, "verify_local_prefix": downloads,
	}
}

// ---------------------------------------------------------------- tests

func TestAudiobookRequestLifecycleTorrentRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	qb := newLifecycleQBittorrent(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)

	// The release on offer is the audio a seeding peer would deliver: six
	// minutes of AAC in an M4B.
	staging := t.TempDir()
	book := filepath.Join(staging, "Synthetic Saga.m4b")
	size := lifecycleAudio(t, tools.ffmpeg, book, 360, "48k")
	hash := "5d41402abc4b2a76b9719d911017c592ab12cd34"
	sources.posts = []lifecycleABBPost{
		{Slug: "saga-sintetica-test-author", Title: "Saga Sintética [Synthetic Saga] - Test Author", Language: "Spanish", Format: "MP3", SizeMB: "300.00", Hash: "1111111111111111111111111111111111111111"},
		{Slug: "synthetic-saga-test-author", Title: lifecycleTitle + " - " + lifecycleAuthor, Language: "English", Format: "M4B", SizeMB: lifecycleMB(size), Hash: hash},
	}
	l.connect(t, lifecycleTorrentConfig(sources, qb, l.downloads), map[string]string{"qbittorrent_password": lifecycleQBPassword})

	// Fresh search, selection and hand-off on the first pass.
	r := l.file(t, lifecycleTitle)
	got := l.run(t, r.ID, false)
	if got.Status != "approved" || lifecycleFulfillment(got) != "queued" {
		t.Fatalf("after hand-off: %+v", got)
	}
	adds, forbidden, logins := qb.snapshot()
	folder := lifecycleMarkerFolder(r, 1)
	if len(adds) != 1 || adds[0].Hash != hash || adds[0].Category != lifecycleCategory || adds[0].Tag != "bloem-"+r.ID+"-a1" || adds[0].SavePath != lifecycleQBRoot+"/"+folder || len(forbidden) != 0 || logins == 0 {
		t.Fatalf("hand-off to qBittorrent: adds=%+v forbidden=%v logins=%d", adds, forbidden, logins)
	}
	searches, details := sources.seen()
	if !slices.Contains(searches, "abb:synthetic saga test author") || !slices.Contains(details, "/abss/synthetic-saga-test-author/") {
		t.Fatalf("source traffic: searches=%v details=%v", searches, details)
	}
	acquisition := l.acquisition(t, r.ID)
	selection := lifecycleDecision(t, acquisition, "select")
	if selection.Outcome != "handed_off" || selection.Chosen == nil || selection.Chosen.Ref != "abb:/abss/synthetic-saga-test-author/" || selection.AI == nil || selection.AI.Used || selection.AICalls != 0 ||
		acquisition.Marker != bookrequests.AudiobookMarker(r.ID, 1) || acquisition.Attempt != 1 || acquisition.SearchPasses != 0 {
		t.Fatalf("selection decision: %+v acquisition=%+v", selection, acquisition)
	}

	// The plugin keeps no state: a restarted process resumes from the
	// host's receipt without searching or adding again.
	l.startPlugin(t)
	qb.setProgress(t, hash, "downloading", 0.42)
	if got = l.run(t, r.ID, true); lifecycleFulfillment(got) != "downloading" {
		t.Fatalf("while downloading: %+v", got)
	}

	// The download finishes: the files appear in the dedicated folder and
	// the plugin verifies them by reading the local copy.
	qb.finish(t, hash, func(dir string) []lifecycleTorrentFile {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(book)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "Synthetic Saga.m4b"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return []lifecycleTorrentFile{{Name: "Synthetic Saga.m4b", Size: int64(len(data)), Progress: 1, Priority: 1}}
	})
	if got = l.run(t, r.ID, true); got.Status != "approved" || lifecycleFulfillment(got) != "awaiting_library" {
		t.Fatalf("after verification: %+v", got)
	}
	verification := lifecycleDecision(t, l.acquisition(t, r.ID), "verify")
	if verification.Outcome != "verified" || len(verification.Issues) != 0 {
		t.Fatalf("verification decision: %+v", verification)
	}
	if searches2, _ := sources.seen(); len(searches2) != len(searches) {
		t.Fatalf("status checks searched again: %v", searches2)
	}

	// The library scan indexes the marker folder; the next pass completes
	// the request with that item and notifies the requester once.
	l.scan(t)
	content, files := l.scannedItem(t, filepath.Join(l.downloads, folder))
	if files != 1 {
		t.Fatalf("scanned files: %d", files)
	}
	if got = l.run(t, r.ID, true); got.Status != "completed" || lifecycleFulfillment(got) != "library" || got.LibraryContentID != content {
		t.Fatalf("after scan: %+v want content %s", got, content)
	}
	l.run(t, r.ID, false)
	l.run(t, r.ID, false)
	events, fulfilled, attention := l.notices.snapshot()
	if !slices.Equal(events, []string{r.ID + ":submitted", r.ID + ":auto_approved"}) || !slices.Equal(fulfilled, []string{r.ID}) || len(attention) != 0 {
		t.Fatalf("notices: lifecycle=%v fulfilled=%v attention=%v", events, fulfilled, attention)
	}
	mine, err := l.books.Get(t.Context(), l.reader, r.ID)
	if err != nil || mine.Status != "completed" || mine.LibraryContentID != content {
		t.Fatalf("requester view: %+v %v", mine, err)
	}
	adds, forbidden, _ = qb.snapshot()
	if len(adds) != 1 || len(forbidden) != 0 {
		t.Fatalf("qBittorrent after completion: adds=%d forbidden=%v", len(adds), forbidden)
	}
	l.requireNoSecrets(t, r.ID, lifecycleQBPassword)
}

func TestAudiobookRequestLifecycleUsenetRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	nzb := newLifecycleNZBGet(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)

	// Two MP3 parts of three minutes each, as an NZB post would unpack.
	staging := t.TempDir()
	parts := []string{"Synthetic Saga - Part 01.mp3", "Synthetic Saga - Part 02.mp3"}
	var total int64
	for _, part := range parts {
		total += lifecycleAudio(t, tools.ffmpeg, filepath.Join(staging, part), 180, "32k")
	}
	nzbID := "9f86d081884c7d659a2feaa0"
	sources.rows = []lifecycleNZBRow{
		{ID: "0a1b2c3d4e5f60718293a4b5", Subject: "Synthetic Saga - Test Author (password) [01/12] - &quot;saga.part01.rar&quot; yEnc", Parts: "12/12", Size: "1.4MB", Password: true},
		{ID: nzbID, Subject: "Synthetic Saga - Test Author [MP3] [01/10] - &quot;Synthetic Saga - Part 01.mp3&quot; yEnc", Parts: "10/10", Size: lifecycleMB(total) + "MB"},
		{ID: "ffeeddccbbaa998877665544", Subject: "Unrelated Thriller - Someone Else [01/40] yEnc", Parts: "40/40", Size: "800MB"},
	}
	l.connect(t, map[string]any{
		"audiobookbay_enabled": false, "nzbking_enabled": true, "nzbking_base_url": sources.server.URL,
		"nzbget_url": nzb.server.URL, "nzbget_username": "bloem", "nzbget_category": lifecycleCategory,
		"allow_http_download_clients": true, "allow_private_source_hosts": true, "allow_loopback_targets": true,
		"verify_min_total_mb": 1, "verify_min_duration_minutes": 5,
		"verify_local_client_prefix": lifecycleNZBRoot, "verify_local_prefix": l.downloads,
	}, map[string]string{"nzbget_password": lifecycleNZBSecret})

	r := l.file(t, lifecycleTitle)
	if got := l.run(t, r.ID, false); lifecycleFulfillment(got) != "queued" {
		searches, _ := sources.seen()
		t.Fatalf("after hand-off: %+v decisions=%+v searches=%v", got, lifecycleDecision(t, l.acquisition(t, r.ID), "select"), searches)
	}
	appends, forbidden := nzb.snapshot()
	folder := lifecycleMarkerFolder(r, 1)
	if len(appends) != 1 || appends[0].URL != sources.server.URL+"/nzb:"+nzbID+"/" || appends[0].Category != lifecycleCategory ||
		appends[0].DupeKey != "bloem-"+r.ID+"-a1" || appends[0].DupeMode != "FORCE" || appends[0].Name != folder+".nzb" || len(forbidden) != 0 {
		t.Fatalf("hand-off to NZBGet: %+v forbidden=%v", appends, forbidden)
	}
	selection := lifecycleDecision(t, l.acquisition(t, r.ID), "select")
	if selection.Outcome != "handed_off" || selection.Chosen == nil || selection.Chosen.Ref != "nzbking:"+nzbID {
		t.Fatalf("selection decision: %+v", selection)
	}
	id := appends[0].ID
	nzb.setStatus(t, id, "DOWNLOADING")
	if got := l.run(t, r.ID, true); lifecycleFulfillment(got) != "downloading" {
		t.Fatalf("while downloading: %+v", got)
	}
	nzb.finish(t, id, func(dir string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, part := range parts {
			data, err := os.ReadFile(filepath.Join(staging, part))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, part), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	if got := l.run(t, r.ID, true); lifecycleFulfillment(got) != "awaiting_library" {
		t.Fatalf("after verification: %+v %+v", got, l.acquisition(t, r.ID).Decisions)
	}
	if verification := lifecycleDecision(t, l.acquisition(t, r.ID), "verify"); verification.Outcome != "verified" {
		t.Fatalf("verification decision: %+v", verification)
	}
	l.scan(t)
	content, files := l.scannedItem(t, filepath.Join(l.downloads, folder))
	if files != len(parts) {
		t.Fatalf("scanned parts: %d", files)
	}
	if got := l.run(t, r.ID, true); got.Status != "completed" || got.LibraryContentID != content {
		t.Fatalf("after scan: %+v want %s", got, content)
	}
	l.run(t, r.ID, false)
	if _, fulfilled, _ := l.notices.snapshot(); !slices.Equal(fulfilled, []string{r.ID}) {
		t.Fatalf("fulfilled notices: %v", fulfilled)
	}
	if appends, forbidden = nzb.snapshot(); len(appends) != 1 || len(forbidden) != 0 {
		t.Fatalf("NZBGet after completion: %+v %v", appends, forbidden)
	}
	l.requireNoSecrets(t, r.ID, lifecycleNZBSecret, "must-not-leave-the-plugin")
}

// A download whose real duration is too short goes to an administrator, who
// can accept it; the scan then completes the request.
func TestAudiobookRequestLifecycleVerificationReviewRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	qb := newLifecycleQBittorrent(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)

	// Two minutes at a high bitrate: large enough, but shorter than the
	// five-minute floor. Only the parsed duration can catch it.
	staging := t.TempDir()
	book := filepath.Join(staging, "Synthetic Saga.m4b")
	size := lifecycleAudio(t, tools.ffmpeg, book, 120, "128k")
	if size < 1<<20 {
		t.Fatalf("fixture too small to isolate the duration check: %d", size)
	}
	hash := "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
	sources.posts = []lifecycleABBPost{{Slug: "synthetic-saga-test-author", Title: lifecycleTitle + " - " + lifecycleAuthor, Language: "English", Format: "M4B", SizeMB: lifecycleMB(size), Hash: hash}}
	l.connect(t, lifecycleTorrentConfig(sources, qb, l.downloads), map[string]string{"qbittorrent_password": lifecycleQBPassword})

	r := l.file(t, lifecycleTitle)
	l.run(t, r.ID, false)
	qb.finish(t, hash, func(dir string) []lifecycleTorrentFile {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(book)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, path.Base(book)), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return []lifecycleTorrentFile{{Name: path.Base(book), Size: int64(len(data)), Progress: 1, Priority: 1}}
	})
	got := l.run(t, r.ID, true)
	acquisition := l.acquisition(t, r.ID)
	verification := lifecycleDecision(t, acquisition, "verify")
	if verification.Outcome != "review" || !slices.Contains(verification.Issues, "too_short") || slices.Contains(verification.Issues, "too_small") {
		t.Fatalf("plugin verification: %+v", verification)
	}
	if got.Status != "approved" || lifecycleFulfillment(got) != "review" || acquisition.ProviderStatus != "review" || acquisition.ReviewKind != "verification" {
		t.Fatalf("a download held for verification review must reach the administrator: request=%+v provider=%s review=%q", got, acquisition.ProviderStatus, acquisition.ReviewKind)
	}
	if _, _, attention := l.notices.snapshot(); !slices.Equal(attention, []string{r.ID + ":1"}) {
		t.Fatalf("attention notices: %v", attention)
	}
	// Review holds: the scan alone does not complete the request.
	l.scan(t)
	if got = l.run(t, r.ID, true); lifecycleFulfillment(got) != "review" {
		t.Fatalf("review released without an administrator: %+v", got)
	}
	accepted, err := l.books.AcceptAudiobookDownload(t.Context(), l.admin, r.ID, bookrequests.ActionInput{ExpectedRevision: got.Revision})
	if err != nil || lifecycleFulfillment(accepted) != "awaiting_library" {
		t.Fatalf("accept download: %+v %v", accepted, err)
	}
	content, _ := l.scannedItem(t, filepath.Join(l.downloads, lifecycleMarkerFolder(r, 1)))
	if got = l.run(t, r.ID, false); got.Status != "completed" || got.LibraryContentID != content {
		t.Fatalf("after acceptance: %+v want %s", got, content)
	}
}

// A confirmed download that disappears from the client is retried with
// back-off and then reported lost; an add whose outcome is unknown is never
// repeated until the client shows it is absent, and the next search adds once.
func TestAudiobookRequestLifecycleLostAndAmbiguousHandoffRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	qb := newLifecycleQBittorrent(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)
	hash := "de9f2c7fd25e1b3afad3e85a0bd17d9b100db4b3"
	sources.posts = []lifecycleABBPost{{Slug: "synthetic-saga-test-author", Title: lifecycleTitle + " - " + lifecycleAuthor, Language: "English", Format: "M4B", SizeMB: "2.00", Hash: hash}}
	l.connect(t, lifecycleTorrentConfig(sources, qb, l.downloads), map[string]string{"qbittorrent_password": lifecycleQBPassword})

	// Ambiguous: qBittorrent answers 503 and never shows the torrent.
	qb.mu.Lock()
	qb.addFails = true
	qb.mu.Unlock()
	r := l.file(t, lifecycleTitle)
	if got := l.run(t, r.ID, false); lifecycleFulfillment(got) != "queued" {
		t.Fatalf("after ambiguous add: %+v", got)
	}
	if a := l.acquisition(t, r.ID); a.ExternalStatus != "unknown" {
		t.Fatalf("ambiguous add external status: %+v", a)
	}
	searches, _ := sources.seen()
	// Two read-only checks find nothing; only then does a new search run,
	// and its preflight would adopt a late add under the same tag.
	for pass := 1; pass <= 2; pass++ {
		l.run(t, r.ID, true)
		if now, _ := sources.seen(); len(now) != len(searches) {
			t.Fatalf("check %d searched again: %v", pass, now)
		}
	}
	if got := l.run(t, r.ID, true); lifecycleFulfillment(got) != "queued" {
		t.Fatalf("after re-search: %+v", got)
	}
	if now, _ := sources.seen(); len(now) != len(searches)+1 {
		t.Fatalf("absent hand-off was not searched again: %v", now)
	}
	adds, forbidden, _ := qb.snapshot()
	if len(adds) != 1 || adds[0].Tag != "bloem-"+r.ID+"-a1" || len(forbidden) != 0 {
		t.Fatalf("adds after recovery: %+v %v", adds, forbidden)
	}
	if a := l.acquisition(t, r.ID); a.Attempt != 1 || a.ExternalStatus != "queued" {
		t.Fatalf("recovered acquisition: %+v", a)
	}

	// Lost: the confirmed torrent is removed from qBittorrent.
	qb.remove(t, hash)
	var got bookrequests.Request
	for range 5 {
		got = l.run(t, r.ID, true)
	}
	if lifecycleFulfillment(got) != "failed" || got.OutcomeReason != "The download could not be found; retry starts a new search." {
		t.Fatalf("a removed download must end as lost: %+v %+v", got, l.acquisition(t, r.ID))
	}
	if _, _, attention := l.notices.snapshot(); !slices.Contains(attention, r.ID+":1") {
		t.Fatalf("lost download raised no attention notice: %v", attention)
	}
	if adds, _, _ = qb.snapshot(); len(adds) != 1 {
		t.Fatalf("lost download was added again: %+v", adds)
	}
}

// An unreachable download client does not fail an in-flight download. The
// request keeps its state through more checks than the lost limit, an outage
// longer than an hour shows in the stuck view, and the download completes
// once the client is back, with one add and no admin notice.
func TestAudiobookRequestLifecycleClientOutageRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	qb := newLifecycleQBittorrent(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)
	book := filepath.Join(t.TempDir(), "Synthetic Saga.m4b")
	size := lifecycleAudio(t, tools.ffmpeg, book, 360, "48k")
	hash := "7c4a8d09ca3762af61e59520943dc26494f8941b"
	sources.setPosts(lifecycleABBPost{Slug: "synthetic-saga-test-author", Title: lifecycleTitle + " - " + lifecycleAuthor, Language: "English", Format: "M4B", SizeMB: lifecycleMB(size), Hash: hash})
	l.connect(t, lifecycleTorrentConfig(sources, qb, l.downloads), map[string]string{"qbittorrent_password": lifecycleQBPassword})

	r := l.file(t, lifecycleTitle)
	l.run(t, r.ID, false)
	qb.setProgress(t, hash, "downloading", 0.3)
	if got := l.run(t, r.ID, true); lifecycleFulfillment(got) != "downloading" {
		t.Fatalf("before the outage: %+v", got)
	}

	qb.setDown(true)
	for pass := 1; pass <= 6; pass++ {
		got, a := l.run(t, r.ID, true), l.acquisition(t, r.ID)
		if got.Status != "approved" || lifecycleFulfillment(got) != "downloading" || a.ProviderStatus != "downloading" || a.ExternalStatus != "unknown" || a.FailureCount != 0 || a.Stuck {
			t.Fatalf("outage pass %d: request=%+v acquisition=%+v", pass, got, a)
		}
	}
	if _, err := l.pool.Exec(t.Context(), `UPDATE bloem_audiobook_request_acquisitions SET updated_at=now()-interval '2 hours' WHERE request_id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	l.run(t, r.ID, true)
	if a := l.acquisition(t, r.ID); a.StuckReason != "unreachable" {
		t.Fatalf("a two-hour outage is not surfaced: %+v", a)
	}

	qb.setDown(false)
	qb.finish(t, hash, func(dir string) []lifecycleTorrentFile {
		return lifecycleCopy(t, dir, map[string]string{"Synthetic Saga.m4b": book})
	})
	if got := l.run(t, r.ID, true); lifecycleFulfillment(got) != "awaiting_library" {
		t.Fatalf("after the outage: %+v %+v", got, l.acquisition(t, r.ID))
	}
	if a := l.acquisition(t, r.ID); a.Stuck {
		t.Fatalf("a readable client is still stuck: %+v", a)
	}
	l.scan(t)
	content, _ := l.scannedItem(t, filepath.Join(l.downloads, lifecycleMarkerFolder(r, 1)))
	if got := l.run(t, r.ID, true); got.Status != "completed" || got.LibraryContentID != content {
		t.Fatalf("after scan: %+v want %s", got, content)
	}
	l.run(t, r.ID, false)
	if _, fulfilled, attention := l.notices.snapshot(); !slices.Equal(fulfilled, []string{r.ID}) || len(attention) != 0 {
		t.Fatalf("notices: fulfilled=%v attention=%v", fulfilled, attention)
	}
	if adds, forbidden, _ := qb.snapshot(); len(adds) != 1 || len(forbidden) != 0 {
		t.Fatalf("qBittorrent after the outage: adds=%+v forbidden=%v", adds, forbidden)
	}
}

// The download folder sits inside a scanned library, so a scan can index a
// finished download before the plugin verifies it. The verifying pass then
// completes from the scanned item; a multi-disc download that scanned as
// several books goes to review on that pass instead.
func TestAudiobookRequestLifecycleScannedBeforeVerificationRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	qb := newLifecycleQBittorrent(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)
	staging := t.TempDir()
	book := filepath.Join(staging, "single", "Synthetic Saga.m4b")
	size := lifecycleAudio(t, tools.ffmpeg, book, 360, "48k")
	discs := map[string]string{"CD1/Track 01.mp3": filepath.Join(staging, "disc", "CD1", "Track 01.mp3"), "CD2/Track 01.mp3": filepath.Join(staging, "disc", "CD2", "Track 01.mp3")}
	var discSize int64
	for _, file := range discs {
		discSize += lifecycleAudio(t, tools.ffmpeg, file, 180, "32k")
	}
	singleHash, discHash := "356a192b7913b04c54574d18c28d46e6395428ab", "da4b9237bacccdf19c0760cab7aec4a8359010b0"
	l.connect(t, lifecycleTorrentConfig(sources, qb, l.downloads), map[string]string{"qbittorrent_password": lifecycleQBPassword})

	sources.setPosts(lifecycleABBPost{Slug: "synthetic-saga-test-author", Title: lifecycleTitle + " - " + lifecycleAuthor, Language: "English", Format: "M4B", SizeMB: lifecycleMB(size), Hash: singleHash})
	single := l.file(t, lifecycleTitle)
	l.run(t, single.ID, false)
	sources.setPosts(lifecycleABBPost{Slug: "disc-saga-test-author", Title: "Disc Saga - " + lifecycleAuthor, Language: "English", Format: "MP3", SizeMB: lifecycleMB(discSize), Hash: discHash})
	disc := l.file(t, "Disc Saga")
	l.run(t, disc.ID, false)
	if adds, _, _ := qb.snapshot(); len(adds) != 2 || adds[0].Hash != singleHash || adds[1].Hash != discHash {
		t.Fatalf("hand-offs: %+v", adds)
	}
	qb.finish(t, singleHash, func(dir string) []lifecycleTorrentFile {
		return lifecycleCopy(t, dir, map[string]string{"Synthetic Saga.m4b": book})
	})
	qb.finish(t, discHash, func(dir string) []lifecycleTorrentFile { return lifecycleCopy(t, dir, discs) })
	l.scan(t)

	content, _ := l.scannedItem(t, filepath.Join(l.downloads, lifecycleMarkerFolder(single, 1)))
	got := l.run(t, single.ID, true)
	if got.Status != "completed" || lifecycleFulfillment(got) != "library" || got.LibraryContentID != content {
		t.Fatalf("the verifying pass did not complete from the scanned item: %+v want %s", got, content)
	}
	if verification := lifecycleDecision(t, l.acquisition(t, single.ID), "verify"); verification.Outcome != "verified" {
		t.Fatalf("plugin verification: %+v", verification)
	}

	got = l.run(t, disc.ID, true)
	a := l.acquisition(t, disc.ID)
	if got.Status != "approved" || lifecycleFulfillment(got) != "review" || a.ProviderStatus != "review" || a.ReviewKind != "verification" {
		t.Fatalf("a multi-disc download must go to review: request=%+v acquisition=%+v", got, a)
	}
	outcomes := []string{}
	for _, record := range a.Decisions {
		if record.Decision.Phase == "verify" {
			outcomes = append(outcomes, record.Decision.Outcome+":"+strings.Join(record.Decision.Issues, ","))
		}
	}
	if !slices.Contains(outcomes, "verified:") || !slices.Contains(outcomes, "review:multiple_items") {
		t.Fatalf("verify decisions: %v", outcomes)
	}
	for range 2 {
		l.run(t, disc.ID, true)
	}
	_, fulfilled, attention := l.notices.snapshot()
	if !slices.Equal(fulfilled, []string{single.ID}) || !slices.Equal(attention, []string{disc.ID + ":1"}) {
		t.Fatalf("notices: fulfilled=%v attention=%v", fulfilled, attention)
	}
	if got = l.run(t, disc.ID, true); lifecycleFulfillment(got) != "review" {
		t.Fatalf("review released without an administrator: %+v", got)
	}
}

// The plugin's verification floor on duration, with real files. A too-short
// MP3 goes to review from its parsed duration. A too-short M4B whose movie
// header follows the audio is the recorded gap: the plugin reads MP4
// durations only from the first 64 KiB, so it verifies that file on size.
func TestAudiobookRequestLifecycleVerificationGapsRealPlugin(t *testing.T) {
	tools := lifecyclePrerequisites(t)
	l := newAudiobookLifecycle(t, tools)
	sources := newLifecycleSources(t)
	qb := newLifecycleQBittorrent(t, l.downloads)
	sources.trustSources(t)
	l.startPlugin(t)
	staging := t.TempDir()
	// Two minutes at a high bitrate: over the 1 MB floor, under five minutes.
	mp3 := filepath.Join(staging, "mp3", "Synthetic Saga.mp3")
	mp3Size := lifecycleAudio(t, tools.ffmpeg, mp3, 120, "128k")
	m4b := filepath.Join(staging, "m4b", "Trailing Header Saga.m4b")
	m4bSize := lifecycleAudioLayout(t, tools.ffmpeg, m4b, 120, "128k", false)
	if mp3Size < 1<<20 || m4bSize < 1<<20 {
		t.Fatalf("fixtures too small to isolate the duration check: %d %d", mp3Size, m4bSize)
	}
	mp3Hash, m4bHash := "77de68daecd823babbb58edb1c8e14d7106e83bb", "1b6453892473a467d07372d45eb05abc2031647a"
	l.connect(t, lifecycleTorrentConfig(sources, qb, l.downloads), map[string]string{"qbittorrent_password": lifecycleQBPassword})

	sources.setPosts(lifecycleABBPost{Slug: "synthetic-saga-test-author", Title: lifecycleTitle + " - " + lifecycleAuthor, Language: "English", Format: "MP3", SizeMB: lifecycleMB(mp3Size), Hash: mp3Hash})
	short := l.file(t, lifecycleTitle)
	l.run(t, short.ID, false)
	sources.setPosts(lifecycleABBPost{Slug: "trailing-header-saga-test-author", Title: "Trailing Header Saga - " + lifecycleAuthor, Language: "English", Format: "M4B", SizeMB: lifecycleMB(m4bSize), Hash: m4bHash})
	trailing := l.file(t, "Trailing Header Saga")
	l.run(t, trailing.ID, false)
	qb.finish(t, mp3Hash, func(dir string) []lifecycleTorrentFile {
		return lifecycleCopy(t, dir, map[string]string{"Synthetic Saga.mp3": mp3})
	})
	qb.finish(t, m4bHash, func(dir string) []lifecycleTorrentFile {
		return lifecycleCopy(t, dir, map[string]string{"Trailing Header Saga.m4b": m4b})
	})

	got := l.run(t, short.ID, true)
	a := l.acquisition(t, short.ID)
	verification := lifecycleDecision(t, a, "verify")
	if verification.Outcome != "review" || !slices.Contains(verification.Issues, "too_short") || slices.Contains(verification.Issues, "too_small") ||
		lifecycleFulfillment(got) != "review" || a.ReviewKind != "verification" {
		t.Fatalf("a too-short MP3 must go to review: verification=%+v request=%+v acquisition=%+v", verification, got, a)
	}

	got = l.run(t, trailing.ID, true)
	verification = lifecycleDecision(t, l.acquisition(t, trailing.ID), "verify")
	// Plugin follow-up: read a trailing MP4 movie header. This assertion then
	// fails; expect a too_short review here instead.
	if verification.Outcome != "verified" || slices.Contains(verification.Issues, "too_short") || lifecycleFulfillment(got) != "awaiting_library" {
		t.Fatalf("the trailing movie header gap changed; if the plugin now reads it, expect a too_short review: verification=%+v request=%+v", verification, got)
	}
}
