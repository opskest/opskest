package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func newTOTPSecret() ([]byte, error) { b := make([]byte, 20); _, err := rand.Read(b); return b, err }
func totpCode(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, secret)
	h.Write(counter[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

// RFC6238, SHA1/6 digits/30 seconds; accept one adjacent time step each way.
func matchTOTP(secret []byte, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	matched := int64(-1)
	step := now.Unix() / 30
	for _, candidate := range []int64{step - 1, step, step + 1} {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, candidate)), []byte(code)) == 1 {
			matched = candidate
		}
	}
	return matched, matched >= 0
}
func recoveryHash(code string) string {
	h := sha256.Sum256([]byte(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))))
	return hex.EncodeToString(h[:])
}
func newRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 10)
	hashes := make([]string, 10)
	for i := range codes {
		raw := make([]byte, 10)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		text := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		codes[i] = text[:4] + "-" + text[4:8] + "-" + text[8:12] + "-" + text[12:]
		hashes[i] = recoveryHash(codes[i])
	}
	return codes, hashes, nil
}
