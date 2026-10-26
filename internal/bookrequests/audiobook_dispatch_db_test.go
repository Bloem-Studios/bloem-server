package bookrequests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type audiobookFake struct {
	mu       sync.Mutex
	fulfill  func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error)
	check    func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error)
	fulfills []AudiobookDispatchInput
	checks   []AudiobookDispatchInput
	receipts []Receipt
	recovers []AudiobookDispatchInput
}

func (f *audiobookFake) Fulfill(ctx context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
	f.mu.Lock()
	f.fulfills = append(f.fulfills, in)
	fn := f.fulfill
	f.mu.Unlock()
	if fn == nil {
		return AudiobookOutcome{}, errors.New("unexpected fulfill")
	}
	return fn(ctx, in)
}

func (f *audiobookFake) CheckStatus(ctx context.Context, in AudiobookDispatchInput, receipt Receipt) (AudiobookOutcome, error) {
	f.mu.Lock()
	f.checks = append(f.checks, in)
	f.receipts = append(f.receipts, receipt)
	fn := f.check
	f.mu.Unlock()
	if fn == nil {
		return AudiobookOutcome{}, errors.New("unexpected check")
	}
	return fn(ctx, in, receipt)
}

func (f *audiobookFake) Recover(_ context.Context, in AudiobookDispatchInput) (Receipt, error) {
	f.mu.Lock()
	f.recovers = append(f.recovers, in)
	f.mu.Unlock()
	return RecoveryReceipt(in.SubmissionID, in.Attempt), nil
}

func (f *audiobookFake) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.fulfills), len(f.checks), len(f.recovers)
}

func (f *audiobookFake) lastFulfill(t *testing.T) AudiobookDispatchInput {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.fulfills) == 0 {
		t.Fatal("no fulfill call")
	}
	return f.fulfills[len(f.fulfills)-1]
}

func (f *audiobookFake) lastCheck(t *testing.T) (AudiobookDispatchInput, Receipt) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.checks) == 0 {
		t.Fatal("no check call")
	}
	return f.checks[len(f.checks)-1], f.receipts[len(f.receipts)-1]
}

type audiobookNoticeSpy struct {
	mu        sync.Mutex
	fulfilled []string
	attention []string
}

func (*audiobookNoticeSpy) Lifecycle(context.Context, Request, string) {}
func (n *audiobookNoticeSpy) Fulfilled(_ context.Context, r Request) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fulfilled = append(n.fulfilled, r.ID)
	return nil
}
func (n *audiobookNoticeSpy) Attention(_ context.Context, r Request, attempt int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.attention = append(n.attention, fmt.Sprintf("%s:%d", r.ID, attempt))
}
func (n *audiobookNoticeSpy) attentions() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.attention)
}

const audiobookRoot = "/library/audiobooks/requests"

type audiobookEnv struct {
	s           *Service
	v           Viewer
	admin       Viewer
	pool        *pgxpool.Pool
	fake        *audiobookFake
	notices     *audiobookNoticeSpy
	integration string
	folder      int
	install     int64
}

func audiobookFixture(t *testing.T) *audiobookEnv {
	t.Helper()
	_, v, pool := databaseFixture(t)
	env := &audiobookEnv{v: v, pool: pool, fake: &audiobookFake{}, notices: &audiobookNoticeSpy{}}
	env.s = NewService(pool, env.notices)
	env.s.SetAudiobookFulfillment(env.fake)
	env.admin = v
	env.admin.Admin = true
	env.integration, env.install = audiobookIntegrationRow(t, pool, "Audiobooks")
	env.folder = audiobookFolder(t, pool, v.OrganizationID, "Audio Books", "audiobooks", "/library/audiobooks")
	if _, err := env.s.SetAudiobookIntegrationLibrary(t.Context(), env.admin, env.integration, env.folder, []string{audiobookRoot}); err != nil {
		t.Fatal(err)
	}
	return env
}

func audiobookIntegrationRow(t *testing.T, pool *pgxpool.Pool, name string) (string, int64) {
	t.Helper()
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var install int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES($1,'1','/fixture',$2) RETURNING id`, AudiobookRequestPluginID, owner).Scan(&install); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO plugin_capabilities(plugin_installation_id,capability_type,capability_id) VALUES($1,'request_router.v1','audiobooks')`, install)
	id := fmt.Sprintf("%d", install*1000000+1)
	bookSQL(t, pool, `INSERT INTO request_integrations(id,name,enabled,base_url,api_key_ref,installation_id,capability_id,supported_media_types,plugin_config) VALUES($1,$2,true,'','abk1.e30',$3,'audiobooks',ARRAY['audiobook'],'{}')`, id, name, install)
	return id, install
}

func audiobookFolder(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, name, kind, path string) int {
	t.Helper()
	var owner uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM resource_owners WHERE organization_id=$1`, org).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(name,type,owner_id) VALUES($1,$2,$3) RETURNING id`, name, kind, owner).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	bookSQL(t, pool, `INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,$2)`, folder, path)
	return folder
}

func (e *audiobookEnv) approve(t *testing.T, in CreateInput) Request {
	t.Helper()
	r, _, err := e.s.Create(t.Context(), e.v, in, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	r, err = e.s.Act(t.Context(), e.admin, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *audiobookEnv) reconcile(t *testing.T) int {
	t.Helper()
	n, err := e.s.ReconcileAudiobooks(t.Context(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *audiobookEnv) due(t *testing.T, id string) {
	t.Helper()
	bookSQL(t, e.pool, `UPDATE bloem_ebook_request_dispatches SET next_attempt_at=now()-interval '1 second' WHERE request_id=$1`, id)
}

func (e *audiobookEnv) get(t *testing.T, id string) Request {
	t.Helper()
	r, err := e.s.Get(t.Context(), e.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type audiobookRow struct {
	started        bool
	externalID     string
	providerStatus string
	failures       int
	attempt        int
	searchPasses   int
	aiCalls        int
	absent         int
	reviewKind     string
	submission     string
	leased         bool
	nextIn         time.Duration
	approved       string
}

func (e *audiobookEnv) row(t *testing.T, id string) audiobookRow {
	t.Helper()
	var r audiobookRow
	var next time.Time
	if err := e.pool.QueryRow(t.Context(), `SELECT d.started,d.external_id,d.provider_status,d.failure_count,a.attempt,a.search_passes,a.ai_calls,a.absent_checks,COALESCE(a.review_kind,''),d.submission_request_id::text,d.lease_token IS NOT NULL,d.next_attempt_at,COALESCE(a.approved_candidate,'')
 FROM bloem_ebook_request_dispatches d JOIN bloem_audiobook_request_acquisitions a ON a.request_id=d.request_id WHERE d.request_id=$1`, id).Scan(
		&r.started, &r.externalID, &r.providerStatus, &r.failures, &r.attempt, &r.searchPasses, &r.aiCalls, &r.absent, &r.reviewKind, &r.submission, &r.leased, &next, &r.approved); err != nil {
		t.Fatal(err)
	}
	r.nextIn = time.Until(next)
	return r
}

func fakeReceipt(in AudiobookDispatchInput) Receipt {
	raw, _ := json.Marshal(AudiobookReceipt{Kind: "qbittorrent", ID: fmt.Sprintf("%040x", in.Attempt), RequestID: in.SubmissionID, Attempt: in.Attempt, Client: "0123456789abcdef", Confirmed: true, Sel: "Synthetic Saga", ExpMB: 512})
	return Receipt{ExternalID: "abr1." + base64.RawURLEncoding.EncodeToString(raw), Status: "queued"}
}

func candidate(ref string) DecisionCandidate {
	return DecisionCandidate{Ref: ref, Source: "audiobookbay", Title: "Synthetic Saga", SizeMB: 512, Format: "M4B", H: 0.9}
}

func handedOff(in AudiobookDispatchInput, ref string) AudiobookOutcome {
	chosen := candidate(ref)
	return AudiobookOutcome{Kind: AudiobookHandedOff, Receipt: fakeReceipt(in), ProviderStatus: "queued", ExternalStatus: "queued",
		Decision: &AcquisitionDecision{V: 1, Phase: "select", Outcome: "handed_off", Chosen: &chosen,
			AI: &DecisionAI{Used: true, Model: "gemini-flash-latest", Choice: "c1", Confidence: 0.86, Reason: "exact title and author", Flags: []string{}}, AICalls: 2}}
}

func selectOutcome(kind string, refs ...string) AudiobookOutcome {
	d := &AcquisitionDecision{V: 1, Phase: "select", Outcome: kind, AICalls: 1}
	for _, ref := range refs {
		d.Candidates = append(d.Candidates, candidate(ref))
	}
	return AudiobookOutcome{Kind: kind, Decision: d}
}

func checkOutcome(provider, external string, verify *AcquisitionDecision) AudiobookOutcome {
	return AudiobookOutcome{Kind: AudiobookStatus, ProviderStatus: provider, ExternalStatus: external, Decision: verify}
}

func verified() *AcquisitionDecision {
	return &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "verified", AICalls: 1}
}

func (e *audiobookEnv) publish(t *testing.T, content, path string) {
	t.Helper()
	bookSQL(t, e.pool, `INSERT INTO media_items(content_id,type,title) VALUES($1,'audiobook','Synthetic Saga') ON CONFLICT DO NOTHING`, content)
	bookSQL(t, e.pool, `INSERT INTO media_files(media_folder_id,file_path,content_id) VALUES($1,$2,$3)`, e.folder, path, content)
}

func markerPath(submission string, attempt int, rest string) string {
	return audiobookRoot + "/Synthetic Saga [" + AudiobookMarker(submission, attempt) + "]/" + rest
}

func TestAudiobookIntegrationScopeAndBindingDatabase(t *testing.T) {
	_, v, pool := databaseFixture(t)
	s := NewService(pool, nil)
	s.SetAudiobookFulfillment(&audiobookFake{})
	admin := v
	admin.Admin = true
	capability := func() string {
		t.Helper()
		c, err := s.Capability(t.Context(), v)
		if err != nil {
			t.Fatal(err)
		}
		return c.FulfilmentBackend
	}
	if got := capability(); got != "unconfigured" {
		t.Fatalf("unconfigured backend: %s", got)
	}
	integration, install := audiobookIntegrationRow(t, pool, "Audiobooks")
	folder := audiobookFolder(t, pool, v.OrganizationID, "Audio Books", "audiobooks", "/library/audiobooks")
	if got := capability(); got != "unconfigured" {
		t.Fatalf("integration without binding is not an opt-in: %s", got)
	}
	ebooks := audiobookFolder(t, pool, v.OrganizationID, "Ebooks", "ebooks", "/library/ebooks")
	for label, attempt := range map[string]func() error{
		"ebook folder": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, ebooks, []string{"/library/ebooks"})
			return err
		},
		"outside library": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{"/srv/other"})
			return err
		},
		"relative": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{"library/audiobooks"})
			return err
		},
		"unclean": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{"/library/audiobooks/../x"})
			return err
		},
		"nested": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{"/library/audiobooks/a", "/library/audiobooks/a/b"})
			return err
		},
		"no roots": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, nil)
			return err
		},
		"not admin": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), v, integration, folder, []string{audiobookRoot})
			return err
		},
		"unknown backend": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, "missing", folder, []string{audiobookRoot})
			return err
		},
		"trailing slash": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{audiobookRoot + "/"})
			return err
		},
		"control": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{audiobookRoot + "\n"})
			return err
		},
		"duplicate roots": func() error {
			_, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{audiobookRoot, audiobookRoot})
			return err
		},
	} {
		if err := attempt(); err == nil {
			t.Errorf("%s binding accepted", label)
		}
	}
	bookSQL(t, pool, `INSERT INTO media_files(media_folder_id,file_path) SELECT $1,'/library/audiobooks/big/'||g||'.mp3' FROM generate_series(1,10001) g`, folder)
	if _, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{"/library/audiobooks/big"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "dedicated") {
		t.Fatalf("crowded root accepted: %v", err)
	}
	bound, err := s.SetAudiobookIntegrationLibrary(t.Context(), admin, integration, folder, []string{audiobookRoot})
	if err != nil || !bound.Ready || bound.LibraryID != folder || bound.BindingRevision != 1 {
		t.Fatalf("binding: %+v %v", bound, err)
	}
	if got := capability(); got != "audiobook_acquisition" {
		t.Fatalf("configured backend: %s", got)
	}
	listed, err := s.ListAudiobookIntegrations(t.Context(), admin)
	if err != nil || len(listed) != 1 || !listed[0].Ready || listed[0].DownloadRoots[0] != audiobookRoot {
		t.Fatalf("list: %+v %v", listed, err)
	}
	libraries, err := s.AudiobookLibraries(t.Context(), admin)
	if err != nil || len(libraries) != 1 || libraries[0].ID != folder || libraries[0].Paths[0] != "/library/audiobooks" {
		t.Fatalf("libraries: %+v %v", libraries, err)
	}
	available := func() bool {
		t.Helper()
		ok, err := AudiobookAcquisitionAvailable(t.Context(), pool, v.OrganizationID)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for label, mutation := range map[string][2]string{
		"no media types":   {`UPDATE request_integrations SET supported_media_types='{}' WHERE id=$1`, `UPDATE request_integrations SET supported_media_types=ARRAY['audiobook'] WHERE id=$1`},
		"extra media type": {`UPDATE request_integrations SET supported_media_types=ARRAY['audiobook','movie'] WHERE id=$1`, `UPDATE request_integrations SET supported_media_types=ARRAY['audiobook'] WHERE id=$1`},
		"service kind":     {`UPDATE request_integrations SET plugin_config='{"service_kind":"radarr"}' WHERE id=$1`, `UPDATE request_integrations SET plugin_config='{}' WHERE id=$1`},
		"disabled":         {`UPDATE request_integrations SET enabled=false WHERE id=$1`, `UPDATE request_integrations SET enabled=true WHERE id=$1`},
		"no key":           {`UPDATE request_integrations SET api_key_ref='' WHERE id=$1`, `UPDATE request_integrations SET api_key_ref='abk1.e30' WHERE id=$1`},
		"plugin disabled":  {`UPDATE plugin_installations SET enabled=false WHERE id=(SELECT installation_id FROM request_integrations WHERE id=$1)`, `UPDATE plugin_installations SET enabled=true WHERE id=(SELECT installation_id FROM request_integrations WHERE id=$1)`},
		"wrong plugin":     {`UPDATE plugin_installations SET plugin_id='bloem.requests.bookwarehouse' WHERE id=(SELECT installation_id FROM request_integrations WHERE id=$1)`, `UPDATE plugin_installations SET plugin_id='bloem.requests.audiobooks' WHERE id=(SELECT installation_id FROM request_integrations WHERE id=$1)`},
		"library disabled": {`UPDATE media_folders SET enabled=false WHERE id=(SELECT media_folder_id FROM bloem_audiobook_request_integrations WHERE integration_id=$1)`, `UPDATE media_folders SET enabled=true WHERE id=(SELECT media_folder_id FROM bloem_audiobook_request_integrations WHERE integration_id=$1)`},
	} {
		bookSQL(t, pool, mutation[0], integration)
		if available() {
			t.Errorf("%s: integration still selectable", label)
		}
		bookSQL(t, pool, mutation[1], integration)
		if !available() {
			t.Fatalf("%s: restore failed", label)
		}
	}
	_ = install
	// D1: a platform integration serves only the default organization.
	hosted, _ := hostedBookViewer(t, pool)
	if ok, err := AudiobookAcquisitionAvailable(t.Context(), pool, hosted.OrganizationID); err != nil || ok {
		t.Fatalf("platform integration served a hosted organization: %v %v", ok, err)
	}
	// Deleting the bound library cascades the binding instead of failing.
	bookSQL(t, pool, `DELETE FROM media_files WHERE media_folder_id=$1`, folder)
	bookSQL(t, pool, `DELETE FROM media_folder_paths WHERE media_folder_id=$1`, folder)
	bookSQL(t, pool, `DELETE FROM media_folders WHERE id=$1`, folder)
	var bindings int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_audiobook_request_integrations`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("binding survived library delete: %d %v", bindings, err)
	}
}

func TestAudiobookHostedOrganizationStaysManualDatabase(t *testing.T) {
	env := audiobookFixture(t)
	hosted, _ := hostedBookViewer(t, env.pool)
	r, _, err := env.s.Create(t.Context(), hosted, CreateInput{Title: "Hosted saga", Author: "Test Author"}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	hostedAdmin := hosted
	hostedAdmin.Admin = true
	if r, err = env.s.Act(t.Context(), hostedAdmin, r.ID, "approve", ActionInput{ExpectedRevision: r.Revision}); err != nil {
		t.Fatal(err)
	}
	env.reconcile(t)
	if f, _, _ := env.fake.counts(); f != 0 {
		t.Fatalf("hosted request dispatched through the platform integration")
	}
	again, err := env.s.Get(t.Context(), hostedAdmin, r.ID)
	if err != nil || again.Fulfilment != "awaiting_fulfilment" {
		t.Fatalf("hosted request: %+v %v", again, err)
	}
}

func TestAudiobookDispatchHappyPathDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.reconcile(t)
	in := env.fake.lastFulfill(t)
	if in.SubmissionID != r.ID || in.Attempt != 1 || in.SearchPass != 1 || in.AICalls != 0 || in.Integration.ID != env.integration || in.Integration.LibraryID != env.folder ||
		!slices.Equal(in.Integration.DownloadRoots, []string{audiobookRoot}) || in.Integration.Language != "en" || len(in.ExcludedRefs) != 0 || in.ApprovedCandidate != "" {
		t.Fatalf("dispatch input: %+v", in)
	}
	if got := env.get(t, r.ID); got.Fulfilment != "queued" || got.Status != "approved" {
		t.Fatalf("queued: %+v", got)
	}
	row := env.row(t, r.ID)
	if !row.started || row.externalID == "" || row.providerStatus != "queued" || row.aiCalls != 2 || row.leased || row.nextIn > audiobookPollDelay {
		t.Fatalf("dispatch row: %+v", row)
	}
	acquisition, err := env.s.AudiobookAcquisition(t.Context(), env.admin, r.ID)
	if err != nil || len(acquisition.Decisions) != 1 || acquisition.Decisions[0].Decision.AI == nil || acquisition.Decisions[0].Decision.AI.Model != "gemini-flash-latest" ||
		acquisition.Marker != AudiobookMarker(r.ID, 1) || acquisition.IntegrationName != "Audiobooks" {
		t.Fatalf("admin acquisition view: %+v %v", acquisition, err)
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if _, receipt := env.fake.lastCheck(t); receipt.ExternalID != row.externalID {
		t.Fatalf("check used another receipt")
	}
	if got := env.get(t, r.ID); got.Fulfilment != "downloading" {
		t.Fatalf("downloading: %+v", got)
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", verified()), nil
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Fulfilment != "awaiting_library" || got.Status != "approved" {
		t.Fatalf("awaiting library: %+v", got)
	}
	_, checks, _ := env.fake.counts()
	env.publish(t, "synthetic-saga", markerPath(r.ID, 1, "Synthetic Saga.m4b"))
	env.due(t, r.ID)
	if n := env.reconcile(t); n != 1 {
		t.Fatalf("path completion count: %d", n)
	}
	if _, again, _ := env.fake.counts(); again != checks {
		t.Fatalf("a verified download was checked again")
	}
	done := env.get(t, r.ID)
	if done.Status != "completed" || done.Fulfilment != "library" || done.LibraryContentID != "synthetic-saga" {
		t.Fatalf("completed: %+v", done)
	}
	for range 2 {
		if _, err := env.s.Reconcile(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
	}
	if len(env.notices.fulfilled) != 1 || env.notices.fulfilled[0] != r.ID || len(env.notices.attentions()) != 0 {
		t.Fatalf("notices: %+v %+v", env.notices.fulfilled, env.notices.attentions())
	}
}

func TestAudiobookPathEvidenceRulesDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.reconcile(t)
	// Files present while the download is unverified never complete it.
	env.publish(t, "early", markerPath(r.ID, 1, "early.m4b"))
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Status == "completed" {
		t.Fatalf("unverified files completed the request: %+v", got)
	}
	bookSQL(t, env.pool, `DELETE FROM media_files WHERE content_id='early'`)
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", verified()), nil
	}
	// Another attempt's marker or a path outside the root is not evidence.
	env.publish(t, "other-attempt", markerPath(r.ID, 2, "x.m4b"))
	env.publish(t, "outside", "/library/audiobooks/Synthetic Saga ["+AudiobookMarker(r.ID, 1)+"]/x.m4b")
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Status == "completed" || got.Fulfilment != "awaiting_library" {
		t.Fatalf("foreign evidence completed the request: %+v", got)
	}
	// Two distinct items under the marker folder are ambiguous.
	env.publish(t, "disc-one", markerPath(r.ID, 1, "CD1/part.mp3"))
	env.publish(t, "disc-two", markerPath(r.ID, 1, "CD2/part.mp3"))
	env.due(t, r.ID)
	env.reconcile(t)
	got := env.get(t, r.ID)
	row := env.row(t, r.ID)
	if got.Fulfilment != "review" || row.providerStatus != "review" || row.reviewKind != "verification" {
		t.Fatalf("multiple items: %+v %+v", got, row)
	}
	if notices := env.notices.attentions(); len(notices) != 1 || notices[0] != r.ID+":1" {
		t.Fatalf("attention: %+v", notices)
	}
	if _, err := env.s.AcceptAudiobookDownload(t.Context(), env.admin, r.ID, ActionInput{ExpectedRevision: got.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("accept-download allowed for multiple items: %v", err)
	}
	if done, err := env.s.Act(t.Context(), env.admin, r.ID, "fulfil", ActionInput{ExpectedRevision: got.Revision}); err != nil || done.Fulfilment != "manual" {
		t.Fatalf("manual fulfil: %+v %v", done, err)
	}
}

func TestAudiobookVerifiedButNotInLibraryIsStuckDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", verified()), nil
	}
	env.reconcile(t)
	env.due(t, r.ID)
	env.reconcile(t)
	bookSQL(t, env.pool, `UPDATE bloem_audiobook_request_acquisitions SET provider_completed_at=now()-interval '25 hours' WHERE request_id=$1`, r.ID)
	for range 2 {
		env.due(t, r.ID)
		env.reconcile(t)
	}
	if notices := env.notices.attentions(); len(notices) != 1 {
		t.Fatalf("verified-but-absent attention: %+v", notices)
	}
	list, err := env.s.ListAudiobookAcquisitions(t.Context(), env.admin, "stuck", "", 10)
	if err != nil || len(list.Acquisitions) != 1 || list.Acquisitions[0].StuckReason != "not_in_library" {
		t.Fatalf("stuck list: %+v %v", list, err)
	}
}

func TestAudiobookNoMatchUnavailableAndUncertainDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return selectOutcome(AudiobookNoMatch), nil
	}
	env.reconcile(t)
	row := env.row(t, r.ID)
	if row.started || row.providerStatus != "searching" || row.searchPasses != 1 || row.nextIn < 5*time.Hour {
		t.Fatalf("no match: %+v", row)
	}
	if got := env.get(t, r.ID); got.Fulfilment != "searching" {
		t.Fatalf("searching: %+v", got)
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if in := env.fake.lastFulfill(t); in.SearchPass != 2 {
		t.Fatalf("second pass: %+v", in)
	}
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return selectOutcome(AudiobookUnavailable), nil
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if row = env.row(t, r.ID); row.nextIn > 31*time.Minute || row.nextIn < 25*time.Minute || row.providerStatus != "searching" {
		t.Fatalf("unavailable: %+v", row)
	}
	// An uncertain Fulfill (the plugin may have added a download) keeps the
	// row started and recovers through reads.
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return AudiobookOutcome{}, errors.New("transport closed")
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if row = env.row(t, r.ID); !row.started || row.failures != 1 || row.providerStatus != "unknown" {
		t.Fatalf("uncertain: %+v", row)
	}
	if got := env.get(t, r.ID); got.Fulfilment != "retrying" {
		t.Fatalf("retrying: %+v", got)
	}
	fulfills, _, _ := env.fake.counts()
	env.due(t, r.ID)
	env.reconcile(t)
	if f, _, recovers := env.fake.counts(); f != fulfills || recovers != 1 {
		t.Fatalf("recovery repeated Fulfill: %d %d", f, recovers)
	}
	if row = env.row(t, r.ID); row.externalID == "" {
		t.Fatalf("unknown receipt not saved: %+v", row)
	}
	env.fake.check = func(_ context.Context, _ AudiobookDispatchInput, receipt Receipt) (AudiobookOutcome, error) {
		parsed, err := ParseAudiobookReceipt(receipt.ExternalID, r.ID, 1)
		if err != nil || parsed.Kind != "unknown" {
			return AudiobookOutcome{}, errors.New("unexpected receipt")
		}
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Fulfilment != "downloading" {
		t.Fatalf("recovered download: %+v", got)
	}
}

func TestAudiobookSearchWindowAndConfigRejectionDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return selectOutcome(AudiobookNoMatch), nil
	}
	env.reconcile(t)
	bookSQL(t, env.pool, `UPDATE bloem_audiobook_request_acquisitions SET attempt_started_at=now()-interval '8 days' WHERE request_id=$1`, r.ID)
	env.due(t, r.ID)
	env.reconcile(t)
	got := env.get(t, r.ID)
	if got.Fulfilment != "failed" || got.OutcomeReason != audiobookReasonWindow || env.row(t, r.ID).providerStatus != "failed" {
		t.Fatalf("search window: %+v", got)
	}
	if notices := env.notices.attentions(); len(notices) != 1 {
		t.Fatalf("failure attention: %+v", notices)
	}
	second := env.approve(t, CreateInput{Title: "Second Saga", Author: "Test Author"})
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return AudiobookOutcome{Kind: AudiobookConfigRejected}, nil
	}
	env.reconcile(t)
	if got := env.get(t, second.ID); got.Fulfilment != "failed" || got.OutcomeReason != audiobookReasonConfig {
		t.Fatalf("config rejected: %+v", got)
	}
}

func TestAudiobookHeldSelectionApprovalDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return selectOutcome(AudiobookHeld, "abb:/abss/synthetic-saga/", "nzbking:0123abcd"), nil
	}
	env.reconcile(t)
	got := env.get(t, r.ID)
	row := env.row(t, r.ID)
	if got.Fulfilment != "review" || got.OutcomeReason != audiobookReasonReview || row.providerStatus != "review" || row.reviewKind != "selection" || row.started {
		t.Fatalf("held: %+v %+v", got, row)
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if f, _, _ := env.fake.counts(); f != 1 {
		t.Fatalf("a held request was searched again: %d", f)
	}
	if notices := env.notices.attentions(); len(notices) != 1 {
		t.Fatalf("held attention: %+v", notices)
	}
	if _, err := env.s.ApproveAudiobookCandidate(t.Context(), env.admin, r.ID, AudiobookCandidateApproval{ExpectedRevision: got.Revision, CandidateRef: "abook:42"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown candidate approved: %v", err)
	}
	if _, err := env.s.ApproveAudiobookCandidate(t.Context(), env.admin, r.ID, AudiobookCandidateApproval{ExpectedRevision: got.Revision, CandidateRef: "https://evil.test/"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed candidate approved: %v", err)
	}
	if _, err := env.s.ApproveAudiobookCandidate(t.Context(), env.v, r.ID, AudiobookCandidateApproval{ExpectedRevision: got.Revision, CandidateRef: "nzbking:0123abcd"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("requester approved a candidate: %v", err)
	}
	approved, err := env.s.ApproveAudiobookCandidate(t.Context(), env.admin, r.ID, AudiobookCandidateApproval{ExpectedRevision: got.Revision, CandidateRef: "nzbking:0123abcd"})
	if err != nil || approved.Fulfilment != "queued" {
		t.Fatalf("approve: %+v %v", approved, err)
	}
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, in.ApprovedCandidate), nil
	}
	env.reconcile(t)
	in := env.fake.lastFulfill(t)
	if in.Attempt != 2 || in.ApprovedCandidate != "nzbking:0123abcd" || in.SubmissionID != r.ID || AudiobookMarker(in.SubmissionID, in.Attempt) == AudiobookMarker(r.ID, 1) {
		t.Fatalf("approved dispatch: %+v", in)
	}
	acquisition, err := env.s.AudiobookAcquisition(t.Context(), env.admin, r.ID)
	if err != nil || !slices.ContainsFunc(acquisition.Decisions, func(d AcquisitionDecisionRecord) bool { return d.Outcome == "admin_approved" && d.Attempt == 2 }) {
		t.Fatalf("admin decision: %+v %v", acquisition.Decisions, err)
	}
}

func TestAudiobookVerificationReviewAcceptDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("review", "review", &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "review", Issues: []string{"abridged"}, AICalls: 1}), nil
	}
	env.reconcile(t)
	env.due(t, r.ID)
	env.reconcile(t)
	got := env.get(t, r.ID)
	if got.Fulfilment != "review" || env.row(t, r.ID).reviewKind != "verification" {
		t.Fatalf("verification review: %+v", got)
	}
	if _, err := env.s.ApproveAudiobookCandidate(t.Context(), env.admin, r.ID, AudiobookCandidateApproval{ExpectedRevision: got.Revision, CandidateRef: "abb:/abss/synthetic-saga/"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("candidate approval accepted for a verification review: %v", err)
	}
	accepted, err := env.s.AcceptAudiobookDownload(t.Context(), env.admin, r.ID, ActionInput{ExpectedRevision: got.Revision})
	if err != nil || accepted.Fulfilment != "awaiting_library" {
		t.Fatalf("accept: %+v %v", accepted, err)
	}
	env.publish(t, "accepted-saga", markerPath(r.ID, 1, "Synthetic Saga.m4b"))
	if n := env.reconcile(t); n != 1 {
		t.Fatalf("accepted download did not complete: %d", n)
	}
	if done := env.get(t, r.ID); done.Fulfilment != "library" || done.LibraryContentID != "accepted-saga" {
		t.Fatalf("completed: %+v", done)
	}
}

func TestAudiobookUnknownMissingAndAbsentDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("unknown", "missing", nil), nil
	}
	env.reconcile(t)
	for i := 1; i <= audiobookMaxFailures; i++ {
		env.due(t, r.ID)
		env.reconcile(t)
		row := env.row(t, r.ID)
		if i < audiobookMaxFailures && (row.failures != i || row.providerStatus != "unknown") {
			t.Fatalf("missing %d: %+v", i, row)
		}
	}
	if got := env.get(t, r.ID); got.Fulfilment != "failed" || got.OutcomeReason != audiobookReasonLost {
		t.Fatalf("lost download: %+v", got)
	}
	absent := env.approve(t, CreateInput{Title: "Absent Saga", Author: "Test Author"})
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("unknown", "absent", nil), nil
	}
	env.reconcile(t)
	fulfills, _, _ := env.fake.counts()
	env.due(t, absent.ID)
	env.reconcile(t)
	if row := env.row(t, absent.ID); row.absent != 1 || !row.started || row.externalID == "" {
		t.Fatalf("absent once: %+v", row)
	}
	env.due(t, absent.ID)
	env.reconcile(t)
	if row := env.row(t, absent.ID); row.absent != 0 || row.started || row.externalID != "" {
		t.Fatalf("absent reset: %+v", row)
	}
	env.due(t, absent.ID)
	env.reconcile(t)
	if f, _, _ := env.fake.counts(); f != fulfills+1 {
		t.Fatalf("absent hand-off was not searched again: %d", f)
	}
}

func TestAudiobookLeaseAndPolicyFencesDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	var nested int
	env.fake.fulfill = func(ctx context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		// A second host cannot claim while the lease is live.
		n, err := env.s.ReconcileAudiobooks(ctx, 100)
		if err != nil {
			return AudiobookOutcome{}, err
		}
		nested = n
		bookSQL(t, env.pool, `UPDATE request_settings SET requests_enabled=false WHERE id`)
		bookSQL(t, env.pool, `UPDATE request_settings SET requests_enabled=true WHERE id`)
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.reconcile(t)
	if f, _, _ := env.fake.counts(); f != 1 || nested != 0 {
		t.Fatalf("second claim during lease: %d %d", f, nested)
	}
	row := env.row(t, r.ID)
	if row.leased || row.providerStatus != "unknown" || row.externalID == "" {
		t.Fatalf("epoch flip applied a stale result or lost the receipt: %+v", row)
	}
	if got := env.get(t, r.ID); got.Fulfilment != "awaiting_fulfilment" {
		t.Fatalf("stale outcome applied: %+v", got)
	}
	// An expired lease drops the late result.
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		bookSQL(t, env.pool, `UPDATE bloem_ebook_request_dispatches SET lease_until=now()-interval '1 second' WHERE request_id=$1`, r.ID)
		return checkOutcome("failed", "client_error", nil), nil
	}
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Fulfilment == "failed" {
		t.Fatalf("expired lease applied a result: %+v", got)
	}
	// With requests disabled nothing is claimed.
	bookSQL(t, env.pool, `UPDATE request_settings SET requests_enabled=false WHERE id`)
	bookSQL(t, env.pool, `UPDATE bloem_ebook_request_dispatches SET lease_token=NULL,lease_until=NULL,next_attempt_at=now()-interval '1 second' WHERE request_id=$1`, r.ID)
	_, checks, _ := env.fake.counts()
	env.reconcile(t)
	if _, again, _ := env.fake.counts(); again != checks {
		t.Fatalf("disabled requests were dispatched")
	}
}

func TestAudiobookRefileWaitsForInFlightFulfillDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	entered, release := make(chan struct{}), make(chan struct{})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		close(entered)
		<-release
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := env.s.ReconcileAudiobooks(context.Background(), 100)
		done <- err
	}()
	<-entered
	current := env.get(t, r.ID)
	if _, err := env.s.Act(t.Context(), env.v, r.ID, "cancel", ActionInput{ExpectedRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	if !env.row(t, r.ID).leased {
		t.Fatal("cancel cleared the in-flight lease")
	}
	refile := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.reconcile(t)
	if f, _, _ := env.fake.counts(); f != 1 {
		t.Fatalf("refile started a second Fulfill during the first: %d", f)
	}
	var dispatched bool
	if err := env.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM bloem_ebook_request_dispatches WHERE request_id=$1)`, refile.ID).Scan(&dispatched); err != nil || dispatched {
		t.Fatalf("refile claimed during the in-flight call: %v %v", dispatched, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.reconcile(t)
	f, checks, _ := env.fake.counts()
	if f != 1 || checks != 1 {
		t.Fatalf("refile did not adopt the hand-off: fulfills=%d checks=%d", f, checks)
	}
	in, receipt := env.fake.lastCheck(t)
	if in.SubmissionID != r.ID || in.Attempt != 1 || receipt.ExternalID != env.row(t, r.ID).externalID {
		t.Fatalf("adopted identity: %+v", in)
	}
	if got := env.get(t, refile.ID); got.Fulfilment != "downloading" {
		t.Fatalf("refile state: %+v", got)
	}
}

func TestAudiobookRetryDuringInFlightCheckDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.reconcile(t)
	bookSQL(t, env.pool, `UPDATE bloem_audiobook_request_acquisitions SET attempt_started_at=now()-interval '80 hours' WHERE request_id=$1`, r.ID)
	entered, release := make(chan struct{}), make(chan struct{})
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		close(entered)
		<-release
		return checkOutcome("downloading", "stalled", &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "review", AICalls: 3}), nil
	}
	done := make(chan error, 1)
	env.due(t, r.ID)
	go func() {
		_, err := env.s.ReconcileAudiobooks(context.Background(), 100)
		done <- err
	}()
	<-entered
	current := env.get(t, r.ID)
	retried, err := env.s.Act(t.Context(), env.admin, r.ID, "retry", ActionInput{ExpectedRevision: current.Revision})
	if err != nil || retried.Fulfilment != "queued" || retried.OutcomeReason != "Starting a new search." {
		t.Fatalf("stuck retry: %+v %v", retried, err)
	}
	if !env.row(t, r.ID).leased {
		t.Fatal("retry cleared the in-flight lease")
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	row := env.row(t, r.ID)
	if row.attempt != 2 || row.aiCalls != 0 || row.externalID != "" || row.providerStatus != "unknown" || row.started {
		t.Fatalf("late attempt-1 result landed in attempt 2: %+v", row)
	}
	// A late attempt-1 receipt never lands in the attempt-2 row.
	late := &audiobookWork{input: AudiobookDispatchInput{Request: r, SubmissionID: r.ID, Attempt: 1}}
	var descriptor []byte
	if err = env.pool.QueryRow(t.Context(), `SELECT descriptor FROM bloem_ebook_request_dispatches WHERE request_id=$1`, r.ID).Scan(&descriptor); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(descriptor, &late.input.Integration); err != nil {
		t.Fatal(err)
	}
	if err = env.s.saveAudiobookReceipt(t.Context(), late, fakeReceipt(late.input)); !errors.Is(err, ErrConflict) {
		t.Fatalf("late receipt saved: %v", err)
	}
}

func TestAudiobookCrossIdentifierRefileAdoptsDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", ISBN: "9780306406157", ASIN: "B012345678"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.reconcile(t)
	current := env.get(t, r.ID)
	if _, err := env.s.Act(t.Context(), env.v, r.ID, "cancel", ActionInput{ExpectedRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	refile := env.approve(t, CreateInput{Title: "Synthetic Saga", ASIN: "B012345678"})
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.reconcile(t)
	if f, checks, _ := env.fake.counts(); f != 1 || checks != 1 {
		t.Fatalf("ASIN refile started a second download: %d %d", f, checks)
	}
	if row := env.row(t, refile.ID); row.submission != r.ID || row.attempt != 1 {
		t.Fatalf("refile did not adopt the submission: %+v", row)
	}
}

func TestAudiobookStaleChainAdoptionDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r1 := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, fmt.Sprintf("abb:/abss/release-%d/", in.Attempt)), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.reconcile(t)
	cancel := func(id string) {
		t.Helper()
		current := env.get(t, id)
		if _, err := env.s.Act(t.Context(), env.v, id, "cancel", ActionInput{ExpectedRevision: current.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	cancel(r1.ID)
	r2 := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("failed", "client_error", nil), nil
	}
	env.reconcile(t)
	got := env.get(t, r2.ID)
	if got.Fulfilment != "failed" || got.OutcomeReason != audiobookReasonFailed {
		t.Fatalf("attempt 1 failure: %+v", got)
	}
	if _, err := env.s.Act(t.Context(), env.admin, r2.ID, "retry", ActionInput{ExpectedRevision: got.Revision}); err != nil {
		t.Fatal(err)
	}
	env.reconcile(t)
	in := env.fake.lastFulfill(t)
	if in.Attempt != 2 || in.SubmissionID != r1.ID || !slices.Equal(in.ExcludedRefs, []string{"abb:/abss/release-1/"}) {
		t.Fatalf("attempt 2 dispatch: %+v", in)
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	cancel(r2.ID)
	r3 := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.reconcile(t)
	check, receipt := env.fake.lastCheck(t)
	parsed, err := ParseAudiobookReceipt(receipt.ExternalID, r1.ID, 2)
	if err != nil || check.Attempt != 2 || parsed.Attempt != 2 || env.row(t, r3.ID).attempt != 2 {
		t.Fatalf("r3 adopted a stale attempt: %+v %+v %v", check, parsed, err)
	}
	if f, _, _ := env.fake.counts(); f != 2 {
		t.Fatalf("extra fulfill: %d", f)
	}
	// After a failed attempt a refile keeps the submission with attempt+1.
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("failed", "deleted", nil), nil
	}
	env.due(t, r3.ID)
	env.reconcile(t)
	cancel(r3.ID)
	r4 := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.reconcile(t)
	in = env.fake.lastFulfill(t)
	if in.SubmissionID != r1.ID || in.Attempt != 3 || env.row(t, r4.ID).attempt != 3 || !slices.Contains(in.ExcludedRefs, "abb:/abss/release-2/") {
		t.Fatalf("refile after failure: %+v", in)
	}
}

func TestAudiobookIdentityOnlyFenceDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "downloading", nil), nil
	}
	env.reconcile(t)
	// Configuration, key, version and binding edits do not fence a download.
	bookSQL(t, env.pool, `UPDATE request_integrations SET plugin_config='{"qbittorrent_category":"books"}',api_key_ref='abk1.e30x',name='Renamed',updated_at=now() WHERE id=$1`, env.integration)
	bookSQL(t, env.pool, `UPDATE plugin_installations SET version='2',updated_at=now() WHERE id=$1`, env.install)
	bookSQL(t, env.pool, `UPDATE bloem_audiobook_request_integrations SET download_roots=ARRAY['/library/audiobooks/elsewhere'] WHERE integration_id=$1`, env.integration)
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Fulfilment != "downloading" {
		t.Fatalf("configuration edit fenced the download: %+v", got)
	}
	bookSQL(t, env.pool, `UPDATE bloem_audiobook_request_integrations SET download_roots=ARRAY[$2] WHERE integration_id=$1`, env.integration, audiobookRoot)
	completed := env.approve(t, CreateInput{Title: "Completed Saga", Author: "Test Author"})
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("completed", "verified", verified()), nil
	}
	env.reconcile(t)
	env.due(t, completed.ID)
	env.reconcile(t)
	// Replacing the capability fails the in-flight download, but a verified
	// one still completes from its frozen roots.
	bookSQL(t, env.pool, `UPDATE plugin_capabilities SET capability_id='other' WHERE plugin_installation_id=$1`, env.install)
	bookSQL(t, env.pool, `UPDATE request_integrations SET capability_id='other' WHERE id=$1`, env.integration)
	env.due(t, r.ID)
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Fulfilment != "failed" || got.OutcomeReason != audiobookReasonBackendChange {
		t.Fatalf("replaced backend: %+v", got)
	}
	var submission string
	if err := env.pool.QueryRow(t.Context(), `SELECT submission_request_id::text FROM bloem_ebook_request_dispatches WHERE request_id=$1`, completed.ID).Scan(&submission); err != nil {
		t.Fatal(err)
	}
	env.publish(t, "completed-saga", markerPath(submission, 1, "a.m4b"))
	env.due(t, completed.ID)
	env.reconcile(t)
	if got := env.get(t, completed.ID); got.Fulfilment != "library" {
		t.Fatalf("verified download after replacement: %+v", got)
	}
}

func TestAudiobookRetryRulesDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	if _, err := env.s.Act(t.Context(), env.admin, r.ID, "retry", ActionInput{ExpectedRevision: r.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry without dispatch: %v", err)
	}
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return AudiobookOutcome{}, errors.New("deadline exceeded")
	}
	env.reconcile(t)
	if got := env.get(t, r.ID); got.Fulfilment != "queued" {
		t.Fatalf("queued: %+v", got)
	}
	if _, err := env.s.Act(t.Context(), env.admin, r.ID, "retry", ActionInput{ExpectedRevision: env.get(t, r.ID).Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry of a healthy download: %v", err)
	}
	env.due(t, r.ID)
	env.reconcile(t)
	got := env.get(t, r.ID)
	if got.Fulfilment != "retrying" || env.row(t, r.ID).failures != 1 {
		t.Fatalf("uncertain check: %+v", got)
	}
	rechecked, err := env.s.Act(t.Context(), env.admin, r.ID, "retry", ActionInput{ExpectedRevision: got.Revision})
	if err != nil || rechecked.Fulfilment != "retrying" || rechecked.OutcomeReason != "Checking the existing download again." {
		t.Fatalf("recheck: %+v %v", rechecked, err)
	}
	if row := env.row(t, r.ID); row.failures != 0 || row.attempt != 1 || row.externalID == "" {
		t.Fatalf("recheck reset: %+v", row)
	}
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("failed", "deleted", &AcquisitionDecision{V: 1, Phase: "verify", Outcome: "review", AICalls: 2}), nil
	}
	env.reconcile(t)
	got = env.get(t, r.ID)
	if got.Fulfilment != "failed" {
		t.Fatalf("failed: %+v", got)
	}
	retried, err := env.s.Act(t.Context(), env.admin, r.ID, "retry", ActionInput{ExpectedRevision: got.Revision})
	if err != nil || retried.Fulfilment != "queued" {
		t.Fatalf("new attempt: %+v %v", retried, err)
	}
	if row := env.row(t, r.ID); row.attempt != 2 || row.aiCalls != 0 || row.searchPasses != 0 || row.failures != 0 || row.started || row.externalID != "" || row.providerStatus != "unknown" {
		t.Fatalf("new attempt counters: %+v", row)
	}
	env.reconcile(t)
	if got = env.get(t, r.ID); got.Fulfilment != "queued" || env.row(t, r.ID).attempt != 2 {
		t.Fatalf("second attempt dispatch: %+v", got)
	}
	// A stalled download can be retried into a new attempt.
	env.fake.check = func(context.Context, AudiobookDispatchInput, Receipt) (AudiobookOutcome, error) {
		return checkOutcome("downloading", "stalled", nil), nil
	}
	env.due(t, r.ID)
	env.reconcile(t)
	got = env.get(t, r.ID)
	if _, err = env.s.Act(t.Context(), env.admin, r.ID, "retry", ActionInput{ExpectedRevision: got.Revision}); err != nil || env.row(t, r.ID).attempt != 3 {
		t.Fatalf("stalled retry: %v %+v", err, env.row(t, r.ID))
	}
	got = env.get(t, r.ID)
	if done, err := env.s.Act(t.Context(), env.admin, r.ID, "fulfil", ActionInput{ExpectedRevision: got.Revision}); err != nil || done.Fulfilment != "manual" {
		t.Fatalf("fulfil on a dispatched request: %+v %v", done, err)
	}
}

func TestAudiobookPerRunLimitsDatabase(t *testing.T) {
	env := audiobookFixture(t)
	ids := []string{}
	for i := range 7 {
		ids = append(ids, env.approve(t, CreateInput{Title: fmt.Sprintf("Saga %d", i), Author: "Test Author"}).ID)
	}
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return selectOutcome(AudiobookNoMatch), nil
	}
	env.reconcile(t)
	if f, _, _ := env.fake.counts(); f != audiobookMaxSearchesPerRun {
		t.Fatalf("searches per run: %d", f)
	}
	var started int
	if err := env.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_ebook_request_dispatches WHERE started OR lease_token IS NOT NULL`).Scan(&started); err != nil || started != 0 {
		t.Fatalf("rows left started or leased: %d %v", started, err)
	}
	// The run budget is checked before each claim.
	clock := time.Now()
	env.s.audiobookNow = func() time.Time {
		now := clock
		clock = clock.Add(2 * time.Minute)
		return now
	}
	bookSQL(t, env.pool, `UPDATE bloem_ebook_request_dispatches SET next_attempt_at=now()-interval '1 second'`)
	before, _, _ := env.fake.counts()
	env.reconcile(t)
	after, _, _ := env.fake.counts()
	if after-before != 1 {
		t.Fatalf("run budget allowed %d claims", after-before)
	}
	if err := env.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_ebook_request_dispatches WHERE started OR lease_token IS NOT NULL`).Scan(&started); err != nil || started != 0 {
		t.Fatalf("rows left started after budget stop: %d %v", started, err)
	}
	_ = ids
}

func TestAudiobookDecisionLogDatabase(t *testing.T) {
	env := audiobookFixture(t)
	r := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(context.Context, AudiobookDispatchInput) (AudiobookOutcome, error) {
		return selectOutcome(AudiobookNoMatch), nil
	}
	env.reconcile(t)
	work := &audiobookWork{input: AudiobookDispatchInput{Request: r, SubmissionID: r.ID, Attempt: 1}}
	for range 60 {
		if err := env.s.saveAudiobookDecision(t.Context(), r.ID, work.input.Attempt, &AcquisitionDecision{V: 1, Phase: "select", Outcome: "no_match", AICalls: 15}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := env.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_audiobook_request_decisions WHERE request_id=$1`, r.ID).Scan(&count); err != nil || count != audiobookMaxDecisions {
		t.Fatalf("decisions kept: %d %v", count, err)
	}
	if row := env.row(t, r.ID); row.aiCalls != 1+60*10 {
		t.Fatalf("ai calls per attempt (clamped per envelope): %d", row.aiCalls)
	}
	// Another attempt's decision is never counted toward this one.
	if err := env.s.saveAudiobookDecision(t.Context(), r.ID, 7, &AcquisitionDecision{V: 1, Phase: "select", Outcome: "no_match", AICalls: 3}); err != nil {
		t.Fatal(err)
	}
	var other int
	if err := env.pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_audiobook_request_decisions WHERE request_id=$1 AND attempt=7`, r.ID).Scan(&other); err != nil || other != 0 {
		t.Fatalf("foreign-attempt decision stored: %d %v", other, err)
	}
}

func TestAudiobookLaneIsolationFromEbooksDatabase(t *testing.T) {
	env := audiobookFixture(t)
	setEbookOptIn(t, env.pool, "true")
	ebookIntegration, ebookFolder, _ := ebookIntegrationFixture(t, env.pool)
	if _, err := env.s.SetEbookIntegrationLibrary(t.Context(), env.admin, ebookIntegration, ebookFolder); err != nil {
		t.Fatal(err)
	}
	ebook := approveEbook(t, env.s, env.v, "9780306406157")
	audio := env.approve(t, CreateInput{Title: "Synthetic Saga", Author: "Test Author"})
	env.fake.fulfill = func(_ context.Context, in AudiobookDispatchInput) (AudiobookOutcome, error) {
		if in.Request.MediaType != "audiobook" {
			return AudiobookOutcome{}, errors.New("ebook in the audiobook lane")
		}
		return handedOff(in, "abb:/abss/synthetic-saga/"), nil
	}
	env.reconcile(t)
	var ebookDispatched bool
	if err := env.pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM bloem_ebook_request_dispatches WHERE request_id=$1)`, ebook.ID).Scan(&ebookDispatched); err != nil || ebookDispatched {
		t.Fatalf("audiobook lane touched an ebook: %v %v", ebookDispatched, err)
	}
	var before, after time.Time
	if err := env.pool.QueryRow(t.Context(), `SELECT updated_at FROM bloem_ebook_request_dispatches WHERE request_id=$1`, audio.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	env.s.SetEbookFulfillment(ebookRouterFixture{fulfill: func(context.Context, DispatchInput) (Receipt, error) {
		return Receipt{ExternalID: "provider-receipt", Status: "queued"}, nil
	}})
	if _, err := env.s.ReconcileEbooks(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	if err := env.pool.QueryRow(t.Context(), `SELECT updated_at FROM bloem_ebook_request_dispatches WHERE request_id=$1`, audio.ID).Scan(&after); err != nil || !after.Equal(before) {
		t.Fatalf("ebook lane touched an audiobook dispatch: %v %v", after, err)
	}
}

func TestAudiobookPathMatchUsesPrefixIndexDatabase(t *testing.T) {
	env := audiobookFixture(t)
	tx, err := env.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err = tx.QueryRow(t.Context(), `EXPLAIN (FORMAT JSON) `+audiobookPathMatchSQL, env.folder, audiobookRoot+"/", audiobookRoot+"0", " [01712151616064439842]/", env.v.OrganizationID).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_media_files_folder_file_path_pattern") {
		t.Fatalf("path match does not use the prefix index:\n%s", plan)
	}
}
