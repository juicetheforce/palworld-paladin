package webserv

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Password hashing uses PBKDF2-HMAC-SHA256 — implemented on the standard
// library only (crypto/hmac + crypto/sha256), so Paladin pulls in no
// external crypto dependency (keeps the single-binary/minimal-deps ethos
// and builds in restricted networks). Format stored:
//
//	pbkdf2$sha256$<iterations>$<base64 salt>$<base64 dk>
//
// Iterations are tunable and embedded in the hash, so the cost can rise in
// future without breaking existing hashes.
const (
	pbkdf2Iterations = 210000 // OWASP-recommended floor for PBKDF2-HMAC-SHA256
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2(password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	return fmt.Sprintf("pbkdf2$sha256$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// verifyPassword is constant-time in the digest comparison.
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "pbkdf2" || parts[1] != "sha256" {
		return false
	}
	iters, err := strconv.Atoi(parts[2])
	if err != nil || iters < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got := pbkdf2(password, salt, iters, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2 implements RFC 2898 PBKDF2 with HMAC-SHA256.
func pbkdf2(password string, salt []byte, iter, keyLen int) []byte {
	prf := func(key, msg []byte) []byte {
		h := hmac.New(sha256.New, key)
		h.Write(msg)
		return h.Sum(nil)
	}
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var dk []byte
	pw := []byte(password)
	for block := 1; block <= numBlocks; block++ {
		// U1 = PRF(password, salt || INT(block))
		blockIdx := []byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)}
		u := prf(pw, append(append([]byte{}, salt...), blockIdx...))
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			u = prf(pw, u)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}
