package webui

import (
	"embed"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:static
var staticFS embed.FS

// assetPathPattern extracts hashed asset URLs the built index.html
// references (used by tests to verify every emitted asset is served).
var assetPathPattern = regexp.MustCompile(`/assets/[A-Za-z0-9._-]+`)

// registerSPA serves the embedded single-page app (built from webui/ into
// static/ by `npm run build`). Unmatched GET paths fall back to index.html
// so deep links like /runs/123 work; API-ish paths keep a JSON 404.
func (h *Handler) registerSPA(r *gin.Engine) {
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet {
			c.Status(http.StatusNotFound)
			return
		}
		p := strings.TrimPrefix(c.Request.URL.Path, "/")
		if p == "" || p == "index.html" {
			h.serveFile(c, "index.html", "text/html; charset=utf-8", false)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			if ct := assetContentType(p); ct != "" {
				h.serveFile(c, p, ct, true)
				return
			}
		}
		if strings.HasPrefix(p, "api/") || strings.HasPrefix(p, "auth/") ||
			strings.HasPrefix(p, "webhooks/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		h.serveFile(c, "index.html", "text/html; charset=utf-8", false)
	})
}

func assetContentType(name string) string {
	switch path.Ext(name) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".woff2":
		return "font/woff2"
	default:
		return ""
	}
}

// serveFile writes an embedded static file. Hashed assets get an immutable
// cache header; index.html stays no-cache so deploys take effect immediately.
func (h *Handler) serveFile(c *gin.Context, name, contentType string, immutable bool) {
	data, err := staticFS.ReadFile("static/" + name)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if immutable {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	c.Data(http.StatusOK, contentType, data)
}
