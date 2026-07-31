package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"schemaVersion":1}`)
	signature := Sign(privateKey, 1234, "event-1", body)
	if err := Verify(publicKey, 1234, "event-1", body, signature); err != nil {
		t.Fatal(err)
	}
	if err := Verify(publicKey, 1234, "event-1", []byte(`{"changed":true}`), signature); err == nil {
		t.Fatal("modified body passed signature verification")
	}
}
