package webhooks

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"agentic-automation/internal/models"

	"github.com/gin-gonic/gin"
)

func TestHandleGitHubWebhookRoutesStatusEvent(t *testing.T) {
	originalHandler := statusEventHandler
	defer func() { statusEventHandler = originalHandler }()

	called := false
	statusEventHandler = func(c *gin.Context) {
		called = true
	}

	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/webhooks/github", bytes.NewBufferString(`{}`))
	c.Request.Header.Set(eventHeader, models.EventTypeStatus)
	c.Request.Header.Set(deliveryHeader, "delivery")
	c.Set("webhook_payload", []byte(`{}`))

	handleGitHubWebhook(c)

	if !called {
		t.Fatalf("expected status event handler to be invoked")
	}
}
