package webui

import (
	"embed"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed static
var staticFS embed.FS

// registerSPA serves the embedded single-page app. Unmatched GET paths fall
// back to index.html so deep links like /runs/123 work; API-ish paths keep a
// JSON 404.
func (h *Handler) registerSPA(r *gin.Engine) {
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Status(http.StatusNotFound)
			return
		}
		path := strings.TrimPrefix(c.Request.URL.Path, "/")
		switch path {
		case "", "index.html":
			h.serveFile(c, "index.html", "text/html; charset=utf-8")
		case "app.js":
			h.serveFile(c, "app.js", "text/javascript; charset=utf-8")
		case "style.css":
			h.serveFile(c, "style.css", "text/css; charset=utf-8")
		default:
			if strings.HasPrefix(path, "api/") || strings.HasPrefix(path, "auth/") ||
				strings.HasPrefix(path, "webhooks/") {
				c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
				return
			}
			h.serveFile(c, "index.html", "text/html; charset=utf-8")
		}
	})
}

func (h *Handler) serveFile(c *gin.Context, name, contentType string) {
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, contentType, data)
}
