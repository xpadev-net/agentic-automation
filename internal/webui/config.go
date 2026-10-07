// Package webui implements the Operator WebUI: GitHub OAuth login, session
// cookies, and authenticated UI API endpoints for browsing AgentRuns and logs.
package webui

import (
	"crypto/sha256"
	"strings"
	"time"

	"agentic-automation/internal/config"
)

// Config holds WebUI configuration loaded from environment variables.
type Config struct {
	// ClientID / ClientSecret authenticate the OAuth flow. The existing GitHub
	// App's client credentials are reused; a dedicated OAuth App also works.
	ClientID     string
	ClientSecret string

	// PublicURL is the externally reachable base URL of the Operator, used to
	// build the OAuth callback URL and the session cookie's scope.
	PublicURL string

	// TokenEncKey encrypts stored OAuth tokens. When empty it is derived from
	// ClientSecret so a separate secret is not strictly required.
	TokenEncKey string

	// SessionTTL is how long a login session stays valid.
	SessionTTL time.Duration

	// OAuthBaseURL and APIBaseURL are overrideable for tests.
	OAuthBaseURL string
	APIBaseURL   string
}

// Enabled reports whether the WebUI auth flow can run. When false the
// Operator behaves exactly as before (no UI routes are registered).
func (c *Config) Enabled() bool {
	return c != nil && c.ClientID != "" && c.ClientSecret != "" && c.PublicURL != ""
}

// CallbackURL returns the OAuth redirect URI registered on the GitHub side.
func (c *Config) CallbackURL() string {
	return strings.TrimRight(c.PublicURL, "/") + "/auth/github/callback"
}

// EncryptionKey derives the 32-byte AES key for token encryption.
func (c *Config) EncryptionKey() [32]byte {
	secret := c.TokenEncKey
	if secret == "" {
		secret = c.ClientSecret
	}
	return sha256.Sum256([]byte(secret))
}

// SecureCookies reports whether cookies should carry the Secure attribute,
// which is the case when PUBLIC_URL uses HTTPS.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(strings.ToLower(c.PublicURL), "https://")
}

// LoadConfig reads WebUI configuration from the environment.
//
//	GITHUB_OAUTH_CLIENT_ID / GITHUB_OAUTH_CLIENT_SECRET: OAuth credentials
//	PUBLIC_URL: externally reachable base URL (shared with log links)
//	UI_TOKEN_ENC_KEY: optional dedicated token encryption key
//	UI_SESSION_TTL_HOURS: session lifetime in hours (default 168 = 7 days)
func LoadConfig() *Config {
	ttl := time.Duration(config.GetEnvInt("UI_SESSION_TTL_HOURS", 168)) * time.Hour
	if ttl <= 0 {
		ttl = 168 * time.Hour
	}
	return &Config{
		ClientID:     config.GetEnv("GITHUB_OAUTH_CLIENT_ID", ""),
		ClientSecret: config.GetEnv("GITHUB_OAUTH_CLIENT_SECRET", ""),
		PublicURL:    strings.TrimRight(config.GetEnv("PUBLIC_URL", ""), "/"),
		TokenEncKey:  config.GetEnv("UI_TOKEN_ENC_KEY", ""),
		SessionTTL:   ttl,
		OAuthBaseURL: config.GetEnv("GITHUB_OAUTH_BASE_URL", "https://github.com"),
		APIBaseURL:   config.GetEnv("GITHUB_API_BASE_URL", "https://api.github.com"),
	}
}


