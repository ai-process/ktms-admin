package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// OWASP's argon2id baseline: 19 MiB, two passes. Verification reads the
// parameters from each hash, so raising them later keeps old hashes valid.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	// MinLength is enforced when hashing, not when signing in, so an
	// operator's existing hash is never locked out by a policy change.
	MinLength = 12
)

var errBadHash = errors.New("unsupported password hash: want argon2id ($argon2id$…) or bcrypt ($2b$…)")

// Hash returns a PHC-format argon2id hash of pw.
func Hash(pw string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, b64(salt), b64(key))
}

// checkHash reports whether h is a hash Verify understands.
func checkHash(h string) error {
	if strings.HasPrefix(h, "$argon2id$") {
		_, err := parseArgon(h)
		return err
	}
	if _, err := bcrypt.Cost([]byte(h)); err == nil {
		return nil
	}
	return errBadHash
}

// Verify compares pw with an argon2id or bcrypt hash in constant time.
func Verify(h, pw string) bool {
	if strings.HasPrefix(h, "$argon2id$") {
		a, err := parseArgon(h)
		if err != nil {
			return false
		}
		got := argon2.IDKey([]byte(pw), a.salt, a.time, a.memory, a.threads, uint32(len(a.key)))
		return subtle.ConstantTimeCompare(got, a.key) == 1
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) == nil
}

type argonHash struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

func parseArgon(h string) (argonHash, error) {
	var a argonHash
	parts := strings.Split(h, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return a, errBadHash
	}
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &a.memory, &a.time, &a.threads)
	if err != nil || parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", a.memory, a.time, a.threads) {
		return a, errBadHash
	}
	var err1, err2 error
	a.salt, err1 = base64.RawStdEncoding.DecodeString(parts[4])
	a.key, err2 = base64.RawStdEncoding.DecodeString(parts[5])
	// Bounds keep a hand-edited hash from asking for gigabytes per attempt.
	if err1 != nil || err2 != nil || len(a.salt) < 8 || len(a.key) < 16 || len(a.key) > 64 ||
		a.time < 1 || a.time > 10 || a.memory < 8*1024 || a.memory > 256*1024 || a.threads < 1 || a.threads > 16 {
		return a, errBadHash
	}
	return a, nil
}
