package palapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Palworld dedicated server's official REST API
// (HTTP Basic auth, username "admin", password = the server's
// AdminPassword). Verified against server v1.0.1.100619.
//
// The API is localhost/LAN-only by Pocketpair's own guidance; this client
// makes no attempt to add transport security because the endpoint must
// never be exposed in the first place (DESIGN.md §8).
type Client struct {
	base string
	pw   string
	hc   *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying *http.Client (e.g. for tests or
// custom timeouts). The default has a 15s timeout.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.hc = hc }
}

// New returns a Client for the given base URL (e.g. "http://127.0.0.1:8212")
// and admin password. The "/v1/api" prefix is appended internally.
func New(baseURL, adminPassword string, opts ...Option) *Client {
	c := &Client{
		base: strings.TrimRight(baseURL, "/") + "/v1/api",
		pw:   adminPassword,
		hc:   &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a non-2xx response from the server.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("palworld api: HTTP %d: %s", e.Status, truncate(e.Body, 200))
}

// Sentinel errors callers can match with errors.Is.
var (
	// ErrUnauthorized: wrong AdminPassword (HTTP 401).
	ErrUnauthorized = errors.New("palworld api: unauthorized (check AdminPassword)")
	// ErrNotAvailable: the endpoint is not served by this server build
	// (HTTP 404). Known case: /game-data is documented but absent on
	// v1.0.1.100619 (DESIGN.md §5.1); re-probe after game updates.
	ErrNotAvailable = errors.New("palworld api: endpoint not available on this server build")
)

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("palapi: encode body: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return fmt.Errorf("palapi: build request: %w", err)
	}
	req.SetBasicAuth("admin", c.pw)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("palapi: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64 MiB cap
	if err != nil {
		return fmt.Errorf("palapi: read response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w (%s)", ErrNotAvailable, path)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &APIError{Status: resp.StatusCode, Body: string(raw)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("palapi: decode %s response: %w", path, err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPost, path, body, nil)
}

// WaitReady polls /info until the server answers 200 or ctx is done.
// This is the "genuine readiness" probe the supervisor and the maintenance
// state machine depend on (DESIGN.md §6.1): REST answering, not merely
// "process exists".
func (c *Client) WaitReady(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := c.Info(pctx)
		cancel()
		if err == nil {
			return nil
		}
		// Wrong password will never self-heal; fail fast rather than
		// spinning until the deadline.
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("palapi: server not ready: %w (last: %v)", ctx.Err(), err)
		case <-t.C:
		}
	}
}

// ProbeGameData reports whether this server build serves /game-data.
// Documented in the official 1.0 docs but verified ABSENT (404) on
// v1.0.1.100619; Paladin re-probes after every game update and swaps the
// live map's data source when it appears (DESIGN.md §5.1, §6.5).
func (c *Client) ProbeGameData(ctx context.Context) (available bool, err error) {
	err = c.get(ctx, "/game-data", nil)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotAvailable):
		return false, nil
	default:
		return false, err
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
