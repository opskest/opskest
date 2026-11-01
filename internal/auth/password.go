package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const passwordIterations = 600000

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)

func NormalizeUsername(username string) string { return strings.ToLower(strings.TrimSpace(username)) }
func ValidateCredentials(username, password string) error {
	if !usernamePattern.MatchString(NormalizeUsername(username)) {
		return errors.New("username must be 3-32 letters, numbers, dots, underscores or hyphens and start with a letter or number")
	}
	return ValidatePassword(password)
}
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || n < 12 || n > 128 || len(password) > 512 {
		return errors.New("password must be 12-128 characters")
	}
	return nil
}

// PBKDF2-HMAC-SHA256, 600k rounds and 128-bit random salts. Format is
// versioned so a future deployment can upgrade hashes without ambiguity.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$600000$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(key), nil
}
func verifyPassword(password, encoded string) bool {
	// Missing accounts still perform the same KDF to avoid a cheap user-existence oracle.
	salt := make([]byte, 16)
	want := make([]byte, 32)
	valid := false
	parts := strings.Split(encoded, "$")
	if len(parts) == 4 && parts[0] == "pbkdf2-sha256" && parts[1] == "600000" {
		s, e1 := hex.DecodeString(parts[2])
		k, e2 := hex.DecodeString(parts[3])
		if e1 == nil && e2 == nil && len(s) == 16 && len(k) == 32 {
			salt = s
			want = k
			valid = true
		}
	}
	if len(password) > 512 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1 && valid
}
