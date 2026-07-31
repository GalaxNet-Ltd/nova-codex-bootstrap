package signing

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
)

var rawURL = base64.RawURLEncoding

func Canonical(timestamp int64, eventID string, body []byte) []byte {
	hash := sha256.Sum256(body)
	return []byte(strconv.FormatInt(timestamp, 10) + "\n" + eventID + "\n" + rawURL.EncodeToString(hash[:]))
}

func Sign(privateKey ed25519.PrivateKey, timestamp int64, eventID string, body []byte) string {
	return rawURL.EncodeToString(ed25519.Sign(privateKey, Canonical(timestamp, eventID, body)))
}

func Verify(publicKey ed25519.PublicKey, timestamp int64, eventID string, body []byte, encodedSignature string) error {
	signature, err := rawURL.DecodeString(encodedSignature)
	if err != nil {
		return errors.New("invalid signature encoding")
	}
	if !ed25519.Verify(publicKey, Canonical(timestamp, eventID, body), signature) {
		return errors.New("invalid signature")
	}
	return nil
}
