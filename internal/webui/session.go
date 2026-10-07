package webui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	// sessionCookieName carries the opaque session token after login.
	sessionCookieName = "aa_session"
	// oauthStateCookieName holds the CSRF state during the OAuth redirect.
	oauthStateCookieName = "aa_oauth_state"
	oauthStateMaxAgeSec  = 600 // 10 minutes

	ctxKeySession = "webui_session"
	ctxKeyLogin   = "webui_login"
)

// randomToken returns a hex-encoded random token of the given byte length.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sessionKey derives the stored session ID from a bearer token. Only the
// SHA-256 digest is persisted, so a database leak cannot replay the cookie.
func sessionKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (h *Handler) setCookie(c *gin.Context, name, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) clearCookie(c *gin.Context, name string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// SessionMiddleware requires a valid WebUI session cookie and stores the
// session in the request context. Requests without one get 401.
func (h *Handler) SessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(sessionCookieName)
		if err != nil || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		session, err := h.sessions.GetByID(sessionKey(token))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				h.clearCookie(c, sessionCookieName)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
				return
			}
			// Transient DB failure: keep the cookie so the user is not
			// forcefully logged out; surface as unavailable instead.
			h.logger.Error("ui_session lookup failed", config.Error(err))
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "session lookup unavailable"})
			return
		}
		if time.Now().After(session.ExpiresAt) {
			_ = h.sessions.Delete(session.ID)
			h.clearCookie(c, sessionCookieName)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
			return
		}
		if err := h.sessions.Touch(session.ID, time.Now()); err != nil {
			h.logger.Warn("Failed to touch ui_session", config.Error(err))
		}
		c.Set(ctxKeySession, session)
		c.Set(ctxKeyLogin, session.GitHubLogin)
		c.Next()
	}
}

// SessionFrom returns the authenticated UISession stored by SessionMiddleware.
func SessionFrom(c *gin.Context) *models.UISession {
	if v, ok := c.Get(ctxKeySession); ok {
		if s, ok := v.(*models.UISession); ok {
			return s
		}
	}
	return nil
}

// GitHubTokenFrom returns the decrypted OAuth access token for the session.
func (h *Handler) GitHubTokenFrom(c *gin.Context) (string, error) {
	session := SessionFrom(c)
	if session == nil {
		return "", errNoSession
	}
	return decryptToken(h.cfg.EncryptionKey(), session.AccessToken)
}
