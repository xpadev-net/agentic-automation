package webui

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"github.com/gin-gonic/gin"
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
		session, err := h.sessions.GetByID(token)
		if err != nil {
			h.clearCookie(c, sessionCookieName)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
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
