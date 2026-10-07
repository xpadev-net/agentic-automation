package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/models"

	"github.com/gin-gonic/gin"
)

var errNoSession = errors.New("no webui session in context")

type tokenExchangeResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
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
	authorizeURL := fmt.Sprintf("%s/login/oauth/authorize?%s", h.cfg.OAuthBaseURL, q.Encode())
	c.Redirect(http.StatusFound, authorizeURL)
}

// HandleGitHubCallback completes the OAuth flow: validates state, exchanges
// the code for a token, resolves the user's login, then creates a session.
func (h *Handler) HandleGitHubCallback(c *gin.Context) {
	if errParam := c.Query("error"); errParam != "" {
		h.logger.Warn("GitHub OAuth returned error",
			config.String("error", errParam),
			config.String("error_description", c.Query("error_description")))
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

	token, err := h.exchangeCode(c.Request.Context(), code)
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
	sessionID, err := randomToken(32)
	if err != nil {
		h.logger.Error("Failed to generate session id", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	session := &models.UISession{
		ID:          sessionID,
		GitHubLogin: login,
		AccessToken: encToken,
		ExpiresAt:   time.Now().Add(h.cfg.SessionTTL),
		LastSeenAt:  time.Now(),
	}
	if err := h.sessions.Create(session); err != nil {
		h.logger.Error("Failed to store ui_session", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	h.setCookie(c, sessionCookieName, sessionID, int(h.cfg.SessionTTL.Seconds()))

	h.logger.Info("WebUI login", config.String("github_login", login))
	c.Redirect(http.StatusFound, "/")
}

// HandleLogout deletes the session and clears the cookie.
func (h *Handler) HandleLogout(c *gin.Context) {
	if token, err := c.Cookie(sessionCookieName); err == nil && token != "" {
		if err := h.sessions.Delete(token); err != nil {
			h.logger.Warn("Failed to delete ui_session", config.Error(err))
		}
	}
	h.clearCookie(c, sessionCookieName)
	c.JSON(http.StatusOK, gin.H{"status": "logged_out"})
}

// exchangeCode trades the OAuth code for a user access token.
func (h *Handler) exchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {h.cfg.ClientID},
		"client_secret": {h.cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {h.cfg.CallbackURL()},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.cfg.OAuthBaseURL+"/login/oauth/access_token", nil)
	if err != nil {
		return "", err
	}
	req.URL.RawQuery = form.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange status %d", resp.StatusCode)
	}
	var out tokenExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("token exchange error: %s (%s)", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return "", errors.New("empty access_token")
	}
	return out.AccessToken, nil
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
