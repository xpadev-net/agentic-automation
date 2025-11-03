package testutils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// ComputeGitHubSignature computes the value for X-Hub-Signature-256 header (sha256=...) for a given secret and payload.
func ComputeGitHubSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sum := mac.Sum(nil)
	return "sha256=" + hex.EncodeToString(sum)
}
