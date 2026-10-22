package secure

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestAuthenticatedEncryption(t *testing.T) {
	v, err := New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"token":"secret"}`)
	a, err := v.Encrypt(plain, "connection:1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := v.Encrypt(plain, "connection:1")
	if bytes.Equal(a, b) || bytes.Contains(a, plain) {
		t.Fatal("nonce reuse or plaintext storage")
	}
	if out, err := v.Decrypt(a, "connection:1"); err != nil || !bytes.Equal(out, plain) {
		t.Fatal(err)
	}
	if _, err = v.Decrypt(a, "connection:2"); err == nil {
		t.Fatal("cross-connection secret accepted")
	}
	a[len(a)-1] ^= 1
	if _, err = v.Decrypt(a, "connection:1"); err == nil {
		t.Fatal("tampering accepted")
	}
}
