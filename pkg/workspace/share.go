package workspace

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// shareTokenEncoding is URL-safe base64 without padding: 32 random bytes
// encode to exactly 43 characters (SEC-04).
var shareTokenEncoding = base64.RawURLEncoding

// generateShareToken returns a cryptographically random share token.
func generateShareToken() (string, error) {
	buf := make([]byte, ShareTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return shareTokenEncoding.EncodeToString(buf), nil
}

// HashShareToken returns the hex SHA-256 of a token; only this hash is
// persisted (SEC-04).
func HashShareToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// validShareToken reports whether token has the exact shape minted by
// generateShareToken. Format failures are indistinguishable from unknown
// tokens at the API layer (both map to 404).
func validShareToken(token string) bool {
	if len(token) != shareTokenEncoding.EncodedLen(ShareTokenBytes) {
		return false
	}
	decoded, err := shareTokenEncoding.DecodeString(token)
	return err == nil && len(decoded) == ShareTokenBytes
}
