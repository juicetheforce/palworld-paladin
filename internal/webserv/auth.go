package webserv

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuthStore holds the single admin credential (DESIGN.md §6.6: one admin
// for v1, but stored as a users table shape so RBAC is additive later).
// Persisted as JSON; the password is bcrypt-hashed, never stored plain.
//
// The web login is decoupled from any OS user and from the game's
// AdminPassword — a forgotten web password is a tool-level reset, never an
// OS operation (§5.2, three-identities rule).
type AuthStore struct {
	path string
	mu   sync.RWMutex
	data authFile
}

type authFile struct {
	// Users is a slice (one row for v1) so adding RBAC is "add rows +
	// a role column", not a schema rewrite (§6.6).
	Users []userRecord `json:"users"`
}

type userRecord struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	Role         string `json:"role"` // "admin" for v1; the RBAC seam
}

// LoadAuthStore opens (or initializes) the auth file at path.
func LoadAuthStore(path string) (*AuthStore, error) {
	s := &AuthStore{path: path}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil // uninitialized; NeedsSetup() will report true
	}
	if err != nil {
		return nil, fmt.Errorf("auth: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("auth: parse %s: %w", path, err)
	}
	return s, nil
}

// NeedsSetup reports whether no admin credential exists yet (first run →
// the UI shows a create-password screen instead of a login screen).
func (s *AuthStore) NeedsSetup() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data.Users) == 0
}

// SetAdminPassword creates or replaces the single admin credential.
func (s *AuthStore) SetAdminPassword(username, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("auth: hash: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Users = []userRecord{{Username: username, PasswordHash: hash, Role: "admin"}}
	return s.persist()
}

// Verify checks a username/password against the stored hash. Constant-time
// username compare + bcrypt (itself constant-time) resist timing probes.
func (s *AuthStore) Verify(username, password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.data.Users {
		if subtle.ConstantTimeCompare([]byte(u.Username), []byte(username)) == 1 {
			return verifyPassword(password, u.PasswordHash)
		}
	}
	// Dummy verify so a missing user costs similar time to a wrong password.
	verifyPassword(password, "pbkdf2$sha256$210000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	return false
}

func (s *AuthStore) persist() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	// 0600: the hash file is sensitive.
	return os.WriteFile(s.path, b, 0o600)
}

// ---- sessions --------------------------------------------------------------

// SessionStore is an in-memory session table. Sessions do not survive a
// restart (acceptable: the operator logs in again). Tokens are random
// 256-bit values delivered as an HttpOnly cookie.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
	ttl      time.Duration
}

type session struct {
	username string
	expires  time.Time
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &SessionStore{sessions: map[string]session{}, ttl: ttl}
}

func (s *SessionStore) New(username string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := fmt.Sprintf("%x", raw)
	s.mu.Lock()
	s.sessions[tok] = session{username: username, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return tok, nil
}

// Valid returns the username for a token if the session is live.
func (s *SessionStore) Valid(tok string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[tok]
	if !ok || time.Now().After(sess.expires) {
		if ok {
			delete(s.sessions, tok)
		}
		return "", false
	}
	return sess.username, true
}

func (s *SessionStore) Delete(tok string) {
	s.mu.Lock()
	delete(s.sessions, tok)
	s.mu.Unlock()
}
