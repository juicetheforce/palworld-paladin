package palapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeServer returns an httptest server that checks basic auth and routes
// a minimal fake of the Palworld REST API. requests records what arrived.
type recorded struct {
	Method string
	Path   string
	Body   map[string]any
}

func fakeServer(t *testing.T, pw string, requests *[]recorded, gameDataPresent bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, got, ok := r.BasicAuth()
		if !ok || user != "admin" || got != pw {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		*requests = append(*requests, recorded{r.Method, r.URL.Path, body})
		switch r.URL.Path {
		case "/v1/api/info":
			json.NewEncoder(w).Encode(map[string]string{
				"version": "v1.0.1.100619", "servername": "Fake",
				"description": "", "worldguid": "ABC123",
			})
		case "/v1/api/players":
			json.NewEncoder(w).Encode(map[string]any{"players": []map[string]any{{
				"name": "Ryan", "playerId": "p1", "userId": "steam_1",
				"ip": "10.0.0.2", "ping": 12.5, "location_x": 1.0,
				"location_y": 2.0, "level": 7,
				"someFutureField": "ignored-by-typed-decode",
			}}})
		case "/v1/api/settings":
			json.NewEncoder(w).Encode(map[string]any{
				"ServerName": "Fake", "ExpRate": 1.0, "BrandNewKey": true,
			})
		case "/v1/api/metrics":
			// Real field names, captured live on v1.0.1.100619.
			json.NewEncoder(w).Encode(map[string]any{
				"serverfps": 59, "serverfpsaverage": 59.73,
				"serverframetime": 16.73, "currentplayernum": 0,
				"maxplayernum": 32, "basecampnum": 4, "days": 2,
				"uptime": 1389, "futuremetric": 1,
			})
		case "/v1/api/game-data":
			if !gameDataPresent {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ActorData": []any{}})
		case "/v1/api/announce", "/v1/api/kick", "/v1/api/ban",
			"/v1/api/unban", "/v1/api/save", "/v1/api/shutdown", "/v1/api/stop":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestAuthAndInfo(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()

	ok := New(srv.URL, "pw")
	info, err := ok.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Version != "v1.0.1.100619" || info.WorldGUID != "ABC123" {
		t.Fatalf("bad decode: %+v", info)
	}

	bad := New(srv.URL, "wrong")
	if _, err := bad.Info(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestPlayersTypedDecodeIgnoresUnknownFields(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	c := New(srv.URL, "pw")
	ps, err := c.Players(context.Background())
	if err != nil {
		t.Fatalf("Players: %v", err)
	}
	if len(ps) != 1 || ps[0].UserID != "steam_1" || ps[0].Level != 7 {
		t.Fatalf("bad players: %+v", ps)
	}
}

func TestSettingsIsPatchTolerantMap(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	c := New(srv.URL, "pw")
	s, err := c.Settings(context.Background())
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if _, ok := s["BrandNewKey"]; !ok {
		t.Fatal("map decode must surface keys the client has never heard of")
	}
}

func TestModerationBodies(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	c := New(srv.URL, "pw")
	ctx := context.Background()

	if err := c.Ban(ctx, "steam_9", "bye"); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if err := c.Unban(ctx, "steam_9"); err != nil {
		t.Fatalf("Unban: %v", err)
	}
	if err := c.Shutdown(ctx, 60, "maintenance"); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	find := func(path string) recorded {
		for _, r := range reqs {
			if r.Path == "/v1/api/"+path {
				return r
			}
		}
		t.Fatalf("no request recorded for %s", path)
		return recorded{}
	}
	if b := find("ban"); b.Body["userid"] != "steam_9" || b.Body["message"] != "bye" {
		t.Fatalf("ban body: %+v", b.Body)
	}
	if u := find("unban"); u.Body["userid"] != "steam_9" {
		t.Fatalf("unban body: %+v", u.Body)
	}
	if s := find("shutdown"); s.Body["waittime"] != float64(60) || s.Body["message"] != "maintenance" {
		t.Fatalf("shutdown body: %+v", s.Body)
	}
}

func TestMetricsTypedDecode(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	c := New(srv.URL, "pw")
	m, err := c.Metrics(context.Background())
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if m.MaxPlayerNum != 32 || m.BaseCampNum != 4 || m.Uptime != 1389 {
		t.Fatalf("bad typed decode: %+v", m)
	}
	raw, err := c.MetricsRaw(context.Background())
	if err != nil {
		t.Fatalf("MetricsRaw: %v", err)
	}
	if _, ok := raw["futuremetric"]; !ok {
		t.Fatal("MetricsRaw must surface unknown fields")
	}
}

func TestProbeGameData(t *testing.T) {
	var reqs []recorded
	absent := fakeServer(t, "pw", &reqs, false)
	defer absent.Close()
	ok, err := New(absent.URL, "pw").ProbeGameData(context.Background())
	if err != nil || ok {
		t.Fatalf("absent probe: available=%v err=%v", ok, err)
	}

	present := fakeServer(t, "pw", &reqs, true)
	defer present.Close()
	ok, err = New(present.URL, "pw").ProbeGameData(context.Background())
	if err != nil || !ok {
		t.Fatalf("present probe: available=%v err=%v", ok, err)
	}
}

func TestGameDataAbsentIsErrNotAvailable(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	_, err := New(srv.URL, "pw").GameData(context.Background())
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("want ErrNotAvailable, got %v", err)
	}
}

func TestWaitReadyFailsFastOnBadPassword(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	c := New(srv.URL, "wrong")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	err := c.WaitReady(ctx, 100*time.Millisecond)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("WaitReady should fail fast on auth errors, not spin")
	}
}

func TestWaitReadySucceedsOnceUp(t *testing.T) {
	var reqs []recorded
	srv := fakeServer(t, "pw", &reqs, false)
	defer srv.Close()
	c := New(srv.URL, "pw")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx, 50*time.Millisecond); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
}
