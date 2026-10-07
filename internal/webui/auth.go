package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"github.com/gin-gonic/gin"
)

var errNoSession = errors.New("no webui session in context")

// sanitizeLogParam strips control characters (CR/LF log forging) and bounds
// the length of client-supplied values before they are written to logs.
func sanitizeLogParam(s string) string {
	const max = 200
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			out = append(out, ' ')
			continue
		}
		out = append(out, r)
	}
	if len(out) > max {
		out = out[:max]
	}
	return string(out)
}

type tokenExchangeResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type githubUserResponse struct {
	Login string `json:"login"`
}

// HandleGitHubLogin starts the OAuth flow: it sets a CSRF state cookie and
// redirects to GitHub's authorize page.
func (h *Handler) HandleGitHubLogin(c *gin.Context) {
	state, err := randomToken(16)
	if err != nil {
		h.logger.Error("Failed to generate OAuth state", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	h.setCookie(c, oauthStateCookieName, state, oauthStateMaxAgeSec)

	q := url.Values{
		"client_id":    {h.cfg.ClientID},
		"redirect_uri": {h.cfg.CallbackURL()},
		"state":        {state},
	}
	if h.cfg.OAuthScope != "" {
		q.Set("scope", h.cfg.OAuthScope)
	}
	authorizeURL := fmt.Sprintf("%s/login/oauth/authorize?%s", h.cfg.OAuthBaseURL, q.Encode())
	c.Redirect(http.StatusFound, authorizeURL)
}

// HandleGitHubCallback completes the OAuth flow: validates state, exchanges
// the code for a token, resolves the user's login, then creates a session.
func (h *Handler) HandleGitHubCallback(c *gin.Context) {
	if errParam := c.Query("error"); errParam != "" {
		h.logger.Warn("GitHub OAuth returned error",
			config.String("error", sanitizeLogParam(errParam)),
			config.String("error_description", sanitizeLogParam(c.Query("error_description"))))
		c.JSON(http.StatusBadRequest, gin.H{"error": "github authorization failed"})
		return
	}

	stateCookie, err := c.Cookie(oauthStateCookieName)
	h.clearCookie(c, oauthStateCookieName)
	if err != nil || stateCookie == "" || stateCookie != c.Query("state") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid oauth state"})
		return
	}
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	token, expiresIn, err := h.exchangeCode(c.Request.Context(), code)
	if err != nil {
		h.logger.Error("OAuth token exchange failed", config.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "token exchange failed"})
		return
	}
	login, err := h.fetchUserLogin(c.Request.Context(), token)
	if err != nil {
		h.logger.Error("Failed to fetch GitHub user", config.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch github user"})
		return
	}

	encToken, err := encryptToken(h.cfg.EncryptionKey(), token)
	if err != nil {
		h.logger.Error("Failed to encrypt token", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	sessionToken, err := randomToken(32)
	if err != nil {
		h.logger.Error("Failed to generate session id", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	expiresAt := time.Now().Add(h.cfg.SessionTTL)
	// Cap the session at the token's actual lifetime: when the GitHub App
	// issues expiring user tokens there is no point keeping a session whose
	// stored token is already dead. refresh_token rotation is tracked in
	// issue #323.
	if expiresIn > 0 {
		if tokenExpiry := time.Now().Add(time.Duration(expiresIn) * time.Second); tokenExpiry.Before(expiresAt) {
			expiresAt = tokenExpiry
		}
	}
	session := &models.UISession{
		ID:          sessionKey(sessionToken),
		GitHubLogin: login,
		AccessToken: encToken,
		ExpiresAt:   expiresAt,
		LastSeenAt:  time.Now(),
	}
	if err := h.sessions.Create(session); err != nil {
		h.logger.Error("Failed to store ui_session", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	h.setCookie(c, sessionCookieName, sessionToken, int(time.Until(expiresAt).Seconds()))

	h.logger.Info("WebUI login", config.String("github_login", login))
	c.Redirect(http.StatusFound, "/")
}

// HandleLogout deletes the session and clears the cookie. When the delete
// fails the cookie is kept so the user can retry; reporting success while
// the server-side session is still valid would leak a live credential.
func (h *Handler) HandleLogout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookieName); err == nil && token != "" {
		if err := h.sessions.Delete(sessionKey(token)); err != nil {
			h.logger.Error("Failed to delete ui_session", config.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "logout failed"})
			return
		}
	}
	h.clearCookie(c, sessionCookieName)
	c.JSON(http.StatusOK, gin.H{"status": "logged_out"})
}

// exchangeCode trades the OAuth code for a user access token. The second
// return value is GitHub's expires_in seconds (0 when the app does not
// issue expiring user tokens).
func (h *Handler) exchangeCode(ctx context.Context, code string) (string, int, error) {
	form := url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {h.cfg.CallbackURL()},
	}
	// Send client credentials in the POST body, never in the request URI:
	// transport errors would otherwise leak client_secret/code into logs
	// (RFC 6749 §2.3.1).
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.cfg.OAuthBaseURL+"/login/oauth/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("token exchange status %d", resp.StatusCode)
	}
	var out tokenExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("decode token response: %w", err)
	}
	if out.Error != "" {
		return "", 0, fmt.Errorf("token exchange error: %s (%s)", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return "", 0, errors.New("empty access_token")
	}
	return out.AccessToken, out.ExpiresIn, nil
}

// fetchUserLogin resolves the authenticated user's login via GET /user.
func (h *Handler) fetchUserLogin(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.cfg.APIBaseURL+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get user status %d", resp.StatusCode)
	}
	var user githubUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", fmt.Errorf("decode user response: %w", err)
	}
	if user.Login == "" {
		return "", errors.New("empty login")
	}
	return user.Login, nil
}
