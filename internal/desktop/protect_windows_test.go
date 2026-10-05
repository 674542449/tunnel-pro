//go:build windows

package desktop

import (
	"bytes"
	"testing"
)

func TestDPAPICredentialsRoundTripAndTamper(t *testing.T) {
	plain := []byte("session-secret-not-in-plaintext")
	sealed, e := protect(plain)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("credential stored in plaintext")
	}
	out, e := unprotect(sealed)
	if e != nil || !bytes.Equal(out, plain) {
		t.Fatal("DPAPI roundtrip failed")
	}
	sealed[len(sealed)-1] ^= 1
	if _, e = unprotect(sealed); e == nil {
		t.Fatal("tampered credential accepted")
	}
}
