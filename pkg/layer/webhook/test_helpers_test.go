package webhook_test

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
)

// hmacSHA1 returns the hex-encoded HMAC-SHA1 of body under secret.
// Used by the GitHub sha1-fallback test.
func hmacSHA1(secret string, body []byte) string {
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
