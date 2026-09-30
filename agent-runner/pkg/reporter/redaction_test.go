package reporter

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestReportBoundariesRedactEveryDiagnosticField(t *testing.T) {
	const secret = "fixture-client-token-1234"
	normal := &ReportRequest{Status: "failed", AgentType: "codex", ErrorMessage: secret, Logs: `{"token":"fixture-json-value"} ` + secret}
	req, err := buildHTTPRequest("https://operator.invalid/report", normal, secret)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if strings.Contains(string(body), secret) || strings.Contains(string(body), "fixture-json-value") {
		t.Fatalf("unsafe report: %s", body)
	}
	if req.Header.Get("Authorization") != "Bearer "+secret {
		t.Fatal("authentication header was changed")
	}
	if normal.ErrorMessage != secret {
		t.Fatal("caller-owned report was mutated")
	}
	plan := &PlanReportRequest{Status: "plan_created", AgentType: "codex", PlanContent: secret, RejectionReason: secret, Logs: secret}
	req, err = buildPlanHTTPRequest("https://operator.invalid/report", plan, secret)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(req.Body)
	if strings.Contains(string(body), secret) {
		t.Fatalf("unsafe plan report: %s", body)
	}
}

func TestResponseCannotEchoClientToken(t *testing.T) {
	for _, status := range []int{200, 400} {
		response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"message":"fixture-direct-client-token"}`))}
		result, err := parseHTTPResponse(response, "fixture-direct-client-token")
		if err != nil && strings.Contains(err.Error(), "fixture-direct-client-token") {
			t.Fatal("token echoed by API reached error")
		}
		if result != nil && strings.Contains(result.Message, "fixture-direct-client-token") {
			t.Fatal("token echoed by API reached success log")
		}
	}
}
