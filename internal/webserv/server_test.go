package webserv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/juicetheforce/palworld-paladin/internal/palapi"
)

type fakeStatus struct {
	info   *palapi.Info
	infErr error
}

func (f fakeStatus) Info(context.Context) (*palapi.Info, error) { return f.info, f.infErr }
func (f fakeStatus) Metrics(context.Context) (*palapi.Metrics, error) {
	return &palapi.Metrics{ServerFPS: 59, ServerFPSAverage: 59.7, CurrentPlayerNum: 3,
		MaxPlayerNum: 32, BaseCampNum: 4, Days: 6, Uptime: 1200}, nil
}

type fakeBackups int

func (f fakeBackups) Count() (int, error) { return int(f), nil }

func newTestServer(t *testing.T) (*Server, *AuthStore) {
	t.Helper()
	auth, err := LoadAuthStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	static := fstest.MapFS{"index.html": {Data: []byte("<html>paladin</html>")}}
	s := New(Config{
		Auth: auth, Sessions: NewSessionStore(0),
		Status:  fakeStatus{info: &palapi.Info{ServerName: "Test", Description: "d", Version: "v1.0.1", WorldGUID: "G"}},
		Backups: fakeBackups(2), Static: static,
	})
	return s, auth
}

func do(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

func TestSessionStateFlow(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()

	// Fresh: needs_setup.
	resp := do(t, h, "GET", "/api/session", "", nil)
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state"] != "needs_setup" {
		t.Fatalf("fresh install must report needs_setup, got %v", body)
	}

	// Setup creates the admin and logs in (returns a session cookie).
	resp = do(t, h, "POST", "/api/setup", `{"username":"admin","password":"hunter2hunter2"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("setup failed: %d", resp.StatusCode)
	}
	ck := sessionCookie(resp)
	if ck == nil {
		t.Fatal("setup must issue a session cookie")
	}

	// With the cookie, session is authenticated.
	resp = do(t, h, "GET", "/api/session", "", ck)
	json.NewDecoder(resp.Body).Decode(&body)
	if body["state"] != "authenticated" {
		t.Fatalf("want authenticated, got %v", body)
	}

	// Setup again is refused.
	resp = do(t, h, "POST", "/api/setup", `{"username":"x","password":"yyyyyyyy"}`, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second setup must 409, got %d", resp.StatusCode)
	}
}

func TestStatusRequiresAuth(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	resp := do(t, h, "GET", "/api/status", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without session must be 401, got %d", resp.StatusCode)
	}
}

func TestLoginAndStatus(t *testing.T) {
	s, auth := newTestServer(t)
	auth.SetAdminPassword("admin", "hunter2hunter2")
	h := s.Handler()

	// Wrong password.
	resp := do(t, h, "POST", "/api/login", `{"username":"admin","password":"wrong"}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password must 401, got %d", resp.StatusCode)
	}
	// Right password.
	resp = do(t, h, "POST", "/api/login", `{"username":"admin","password":"hunter2hunter2"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login failed: %d", resp.StatusCode)
	}
	ck := sessionCookie(resp)

	// Status with session returns the dashboard payload.
	resp = do(t, h, "GET", "/api/status", "", ck)
	if resp.StatusCode != 200 {
		t.Fatalf("status failed: %d", resp.StatusCode)
	}
	var st StatusResponse
	json.NewDecoder(resp.Body).Decode(&st)
	if !st.Online || st.ServerName != "Test" || st.Players != 3 || st.MaxPlayers != 32 || st.BackupCount != 2 {
		t.Fatalf("bad status payload: %+v", st)
	}
}

func TestStatusReportsOfflineGracefully(t *testing.T) {
	auth, _ := LoadAuthStore(filepath.Join(t.TempDir(), "auth.json"))
	auth.SetAdminPassword("admin", "hunter2hunter2")
	s := New(Config{
		Auth: auth, Sessions: NewSessionStore(0),
		Status:  fakeStatus{infErr: context.DeadlineExceeded},
		Backups: fakeBackups(0), Static: fstest.MapFS{"index.html": {Data: []byte("x")}},
	})
	h := s.Handler()
	resp := do(t, h, "POST", "/api/login", `{"username":"admin","password":"hunter2hunter2"}`, nil)
	ck := sessionCookie(resp)
	resp = do(t, h, "GET", "/api/status", "", ck)
	if resp.StatusCode != 200 {
		t.Fatalf("offline server must still 200 (reportable state), got %d", resp.StatusCode)
	}
	var st StatusResponse
	json.NewDecoder(resp.Body).Decode(&st)
	if st.Online || st.Error == "" {
		t.Fatalf("offline must be reported in-band: %+v", st)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	s, auth := newTestServer(t)
	auth.SetAdminPassword("admin", "hunter2hunter2")
	h := s.Handler()
	resp := do(t, h, "POST", "/api/login", `{"username":"admin","password":"hunter2hunter2"}`, nil)
	ck := sessionCookie(resp)
	do(t, h, "POST", "/api/logout", "", ck)
	resp = do(t, h, "GET", "/api/status", "", ck)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("session must be invalid after logout")
	}
}

func TestSPAFallback(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	// An unknown client route serves index.html, not 404.
	resp := do(t, h, "GET", "/players", "", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("SPA route must serve index.html, got %d", resp.StatusCode)
	}
}

// ---- players ----

type fakePlayers struct{ kicked, banned, unbanned string }

func (f *fakePlayers) Players(context.Context) ([]palapi.Player, error) {
	return []palapi.Player{{Name: "Ryan", UserID: "steam_1", Level: 42, Ping: 20}}, nil
}
func (f *fakePlayers) Kick(_ context.Context, id, _ string) error { f.kicked = id; return nil }
func (f *fakePlayers) Ban(_ context.Context, id, _ string) error  { f.banned = id; return nil }
func (f *fakePlayers) Unban(_ context.Context, id string) error   { f.unbanned = id; return nil }

func newPlayersServer(t *testing.T) (*Server, *AuthStore, *fakePlayers) {
	t.Helper()
	auth, _ := LoadAuthStore(filepath.Join(t.TempDir(), "auth.json"))
	auth.SetAdminPassword("admin", "hunter2hunter2")
	fp := &fakePlayers{}
	s := New(Config{
		Auth: auth, Sessions: NewSessionStore(0),
		Status:  fakeStatus{info: &palapi.Info{ServerName: "T"}},
		Players: fp,
		BanList: func() ([]palapi.BanEntry, error) { return []palapi.BanEntry{{UserID: "steam_9"}}, nil },
		Static:  fstest.MapFS{"index.html": {Data: []byte("x")}},
	})
	return s, auth, fp
}

func authedCookie(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	resp := do(t, h, "POST", "/api/login", `{"password":"hunter2hunter2"}`, nil)
	return sessionCookie(resp)
}

func TestPlayersRosterReservedFields(t *testing.T) {
	s, _, _ := newPlayersServer(t)
	h := s.Handler()
	ck := authedCookie(t, h)
	resp := do(t, h, "GET", "/api/players", "", ck)
	var body playersResponse
	json.NewDecoder(resp.Body).Decode(&body)
	if !body.Online || len(body.Players) != 1 {
		t.Fatalf("bad roster: %+v", body)
	}
	p := body.Players[0]
	if p.Level != 42 || p.Guild != nil || p.Bases != nil {
		t.Fatalf("online tier should populate level; guild/bases reserved-null: %+v", p)
	}
	if body.HistoryTier {
		t.Fatal("history tier must be false until save parsing exists")
	}
}

func TestPlayerActions(t *testing.T) {
	s, _, fp := newPlayersServer(t)
	h := s.Handler()
	ck := authedCookie(t, h)
	do(t, h, "POST", "/api/players/kick", `{"user_id":"steam_1"}`, ck)
	do(t, h, "POST", "/api/players/ban", `{"user_id":"steam_2"}`, ck)
	do(t, h, "POST", "/api/players/unban", `{"user_id":"steam_9"}`, ck)
	if fp.kicked != "steam_1" || fp.banned != "steam_2" || fp.unbanned != "steam_9" {
		t.Fatalf("actions not dispatched: %+v", fp)
	}
	// Missing user_id is a 400.
	resp := do(t, h, "POST", "/api/players/kick", `{}`, ck)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing user_id must 400, got %d", resp.StatusCode)
	}
}

func TestPlayerActionsRequireAuth(t *testing.T) {
	s, _, _ := newPlayersServer(t)
	resp := do(t, s.Handler(), "POST", "/api/players/kick", `{"user_id":"x"}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("moderation must require auth, got %d", resp.StatusCode)
	}
}

// ---- server admin ----

type fakeLifecycle struct{ started, stopped, restarted bool }

func (f *fakeLifecycle) Start(context.Context) error   { f.started = true; return nil }
func (f *fakeLifecycle) Stop(context.Context) error    { f.stopped = true; return nil }
func (f *fakeLifecycle) Restart(context.Context) error { f.restarted = true; return nil }

type fakeBroadcaster struct {
	announced string
	saved     bool
}

func (f *fakeBroadcaster) Announce(_ context.Context, m string) error { f.announced = m; return nil }
func (f *fakeBroadcaster) Save(context.Context) error                 { f.saved = true; return nil }

func newAdminServer(t *testing.T) (*Server, *fakeLifecycle, *fakeBroadcaster) {
	t.Helper()
	auth, _ := LoadAuthStore(filepath.Join(t.TempDir(), "auth.json"))
	auth.SetAdminPassword("admin", "hunter2hunter2")
	lc := &fakeLifecycle{}
	bc := &fakeBroadcaster{}
	s := New(Config{
		Auth: auth, Sessions: NewSessionStore(0),
		Status:    fakeStatus{info: &palapi.Info{ServerName: "T"}},
		Lifecycle: lc, Broadcaster: bc,
		Static: fstest.MapFS{"index.html": {Data: []byte("x")}},
	})
	return s, lc, bc
}

func TestLifecycleActions(t *testing.T) {
	s, lc, _ := newAdminServer(t)
	h := s.Handler()
	ck := authedCookie(t, h)
	do(t, h, "POST", "/api/admin/lifecycle/start", `{}`, ck)
	do(t, h, "POST", "/api/admin/lifecycle/restart", `{}`, ck)
	do(t, h, "POST", "/api/admin/lifecycle/stop", `{}`, ck)
	if !lc.started || !lc.restarted || !lc.stopped {
		t.Fatalf("lifecycle not dispatched: %+v", lc)
	}
}

func TestLifecycleWithBroadcast(t *testing.T) {
	s, lc, bc := newAdminServer(t)
	h := s.Handler()
	ck := authedCookie(t, h)
	do(t, h, "POST", "/api/admin/lifecycle/restart", `{"broadcast":"heads up","delay_seconds":0}`, ck)
	if bc.announced != "heads up" || !lc.restarted {
		t.Fatalf("broadcast+restart failed: announced=%q restarted=%v", bc.announced, lc.restarted)
	}
}

func TestBroadcastAndSave(t *testing.T) {
	s, _, bc := newAdminServer(t)
	h := s.Handler()
	ck := authedCookie(t, h)
	do(t, h, "POST", "/api/admin/broadcast", `{"message":"hello all"}`, ck)
	do(t, h, "POST", "/api/admin/save", ``, ck)
	if bc.announced != "hello all" || !bc.saved {
		t.Fatalf("broadcast/save failed: %+v", bc)
	}
	// empty broadcast → 400
	resp := do(t, h, "POST", "/api/admin/broadcast", `{"message":""}`, ck)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty broadcast must 400, got %d", resp.StatusCode)
	}
}

func TestHistoryRecordsActions(t *testing.T) {
	s, _, _ := newAdminServer(t)
	h := s.Handler()
	ck := authedCookie(t, h)
	do(t, h, "POST", "/api/admin/broadcast", `{"message":"logged"}`, ck)
	resp := do(t, h, "GET", "/api/admin/history", "", ck)
	var body struct{ History []LogEntry }
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.History) == 0 || body.History[0].Action != "broadcast" {
		t.Fatalf("history not recorded: %+v", body.History)
	}
}

func TestAdminRequiresAuth(t *testing.T) {
	s, _, _ := newAdminServer(t)
	resp := do(t, s.Handler(), "POST", "/api/admin/lifecycle/stop", `{}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin must require auth, got %d", resp.StatusCode)
	}
}
