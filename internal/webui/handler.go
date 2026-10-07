package webui

import (
	"net/http"
	"sync"
	"time"

	"agentic-automation/internal/config"
	"agentic-automation/internal/repositories"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Handler bundles WebUI HTTP handlers and their dependencies.
type Handler struct {
	cfg        *Config
	db         *gorm.DB
	sessions   *repositories.UISessionRepository
	logs       *repositories.AgentRunLogRepository
	perms      *permChecker
	logger     *config.AppLogger
	httpClient *http.Client
}

// NewHandler builds a Handler. The returned cleanup stop function should be
// invoked on shutdown to stop the session/log janitor.
func NewHandler(cfg *Config, db *gorm.DB, logger *config.AppLogger) (*Handler, func()) {
	h := &Handler{
		cfg:        cfg,
		db:         db,
		sessions:   repositories.NewUISessionRepository(db),
		logs:       repositories.NewAgentRunLogRepository(db),
		perms:      newPermChecker(cfg.APIBaseURL),
		logger:     logger,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
	return h, h.startJanitor()
}

// RegisterRoutes mounts auth and UI API routes on the router.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.GET("/auth/github/login", h.HandleGitHubLogin)
	r.GET("/auth/github/callback", h.HandleGitHubCallback)
	r.POST("/auth/logout", h.HandleLogout)

	ui := r.Group("/api/ui", h.SessionMiddleware())
	ui.GET("/me", h.HandleMe)
	ui.GET("/runs", h.HandleListRuns)
	ui.GET("/runs/:id", h.HandleGetRun)
	ui.GET("/runs/:id/logs", h.HandleGetRunLogs)
	ui.GET("/runs/:id/logs/stream", h.HandleStreamRunLogs)
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

// startJanitor periodically deletes expired sessions and, when retention is
// enabled, old persisted log lines. The returned stop hook is idempotent.
func (h *Handler) startJanitor() func() {
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		cleanup := func(now time.Time) {
			deleted, err := h.sessions.DeleteExpired(now)
			if err != nil {
				h.logger.Warn("ui_session cleanup failed", config.Error(err))
			} else if deleted > 0 {
				h.logger.Info("ui_session cleanup removed expired sessions",
					config.Int("deleted", int(deleted)))
			}
			if h.cfg.LogRetentionDays > 0 {
				cutoff := now.Add(-time.Duration(h.cfg.LogRetentionDays) * 24 * time.Hour)
				deleted, err := h.logs.DeleteOlderThan(cutoff)
				if err != nil {
					h.logger.Warn("agent_run_logs retention cleanup failed", config.Error(err))
				} else if deleted > 0 {
					h.logger.Info("agent_run_logs retention removed old logs",
						config.Int("deleted", int(deleted)))
				}
			}
		}
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				cleanup(now)
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
