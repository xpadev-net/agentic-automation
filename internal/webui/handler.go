package webui

import (
	"net/http"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler bundles WebUI HTTP handlers and their dependencies.
type Handler struct {
	cfg        *Config
	sessions   *repositories.UISessionRepository
	logger     *config.AppLogger
	httpClient *http.Client
}

// NewHandler builds a Handler. The returned cleanup stop function should be
// invoked on shutdown to stop the session-expiry janitor.
func NewHandler(cfg *Config, db *gorm.DB, logger *config.AppLogger) (*Handler, func()) {
	h := &Handler{
		cfg:        cfg,
		sessions:   repositories.NewUISessionRepository(db),
		logger:     logger,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	return h, h.startSessionJanitor()
}

// RegisterRoutes mounts auth and UI API routes on the router.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.GET("/auth/github/login", h.HandleGitHubLogin)
	r.GET("/auth/github/callback", h.HandleGitHubCallback)
	r.POST("/auth/logout", h.HandleLogout)

	ui := r.Group("/api/ui", h.SessionMiddleware())
	ui.GET("/me", h.HandleMe)
}

// HandleMe returns the authenticated user's identity.
func (h *Handler) HandleMe(c *gin.Context) {
	session := SessionFrom(c)
	if session == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"login":      session.GitHubLogin,
		"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// startSessionJanitor periodically deletes expired sessions.
func (h *Handler) startSessionJanitor() func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				deleted, err := h.sessions.DeleteExpired(now)
				if err != nil {
					h.logger.Warn("ui_session cleanup failed", config.Error(err))
				} else if deleted > 0 {
					h.logger.Info("ui_session cleanup removed expired sessions",
						config.Int("deleted", int(deleted)))
				}
			}
		}
	}()
	return func() { close(stop) }
}
