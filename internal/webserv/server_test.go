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
