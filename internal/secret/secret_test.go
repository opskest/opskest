package secret

import (
	"bytes"
	"testing"
)

func key(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	box, err := New(key(1))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := []byte("super secret token")
	sealed, err := box.Seal(plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plain) {
		t.Fatalf("round trip mismatch: got %q want %q", opened, plain)
	}
}

func TestTamperFails(t *testing.T) {
	box, err := New(key(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := box.Seal([]byte("hello world"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := box.Open(tampered); err == nil {
		t.Fatal("expected error opening tampered ciphertext")
	}
}

func TestNonceUnique(t *testing.T) {
	box, err := New(key(3))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := []byte("same plaintext")
	a, err := box.Seal(plain)
	if err != nil {
		t.Fatalf("Seal a: %v", err)
	}
	b, err := box.Seal(plain)
	if err != nil {
		t.Fatalf("Seal b: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext must differ (nonce reuse)")
	}
}

func TestWrongKeyFails(t *testing.T) {
	box1, err := New(key(4))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	box2, err := New(key(5))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := box1.Seal([]byte("payload"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := box2.Open(sealed); err == nil {
		t.Fatal("expected error opening with wrong key")
	}
}

func TestJSON(t *testing.T) {
	box, err := New(key(6))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	in := map[string]string{"authorization": "Bearer xyz", "x-api-key": "abc123"}
	sealed, err := box.SealJSON(in)
	if err != nil {
		t.Fatalf("SealJSON: %v", err)
	}
	var out map[string]string
	if err := box.OpenJSON(sealed, &out); err != nil {
		t.Fatalf("OpenJSON: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len mismatch: got %v want %v", out, in)
	}
	for k, v := range in {
		if out[k] != v {
			t.Fatalf("key %q: got %q want %q", k, out[k], v)
		}
	}
}
