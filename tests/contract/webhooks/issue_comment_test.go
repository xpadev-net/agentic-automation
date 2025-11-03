package webhooks_contract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentic-automation/internal/webhooks/handlers"
)

// Contract reference:
// - specs/001-github-agent-automation/contracts/github-webhooks.md (issue_comment payload 28-61)

// loadFixture reads a JSON fixture from tests/fixtures/webhooks/
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	// From tests/contract/webhooks → tests/fixtures/webhooks
	base := filepath.Join("..", "..", "fixtures", "webhooks")
	data, err := os.ReadFile(filepath.Join(base, name))
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", name, err)
	}
	return data
}

// validateIssueCommentRequiredFields checks presence of required fields in the payload struct
func validateIssueCommentRequiredFields(p handlers.IssueCommentPayload) error {
	if p.Action == "" {
		return errors.New("missing action")
	}
	if p.Issue.ID == 0 {
		return errors.New("missing issue.id")
	}
	if p.Issue.Number == 0 {
		return errors.New("missing issue.number")
	}
	if p.Issue.State == "" {
		return errors.New("missing issue.state")
	}
	if p.Issue.User.Login == "" {
		return errors.New("missing issue.user.login")
	}
	if p.Comment.ID == 0 {
		return errors.New("missing comment.id")
	}
	if p.Comment.Body == "" {
		return errors.New("missing comment.body")
	}
	if p.Comment.User.Login == "" {
		return errors.New("missing comment.user.login")
	}
	if p.Repository.FullName == "" {
		return errors.New("missing repository.full_name")
	}
	if p.Repository.Owner.Login == "" {
		return errors.New("missing repository.owner.login")
	}
	return nil
}

// validateIssueCommentValueRules verifies contract value constraints
func validateIssueCommentValueRules(p handlers.IssueCommentPayload) error {
	if p.Action != "created" {
		return errors.New("action must be 'created'")
	}
	if strings.ToLower(p.Issue.State) != "open" {
		return errors.New("issue.state must be 'open'")
	}
	// repository.full_name must be of the form owner/repo (exactly one slash)
	if parts := strings.Split(p.Repository.FullName, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("repository.full_name must be 'owner/repo'")
	}
	return nil
}

func Test_Unmarshal_ValidPayload(t *testing.T) {
	data := loadFixture(t, "issue_comment_trigger.json")
	var p handlers.IssueCommentPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if err := validateIssueCommentRequiredFields(p); err != nil {
		t.Fatalf("required fields validation failed: %v", err)
	}
	if err := validateIssueCommentValueRules(p); err != nil {
		t.Fatalf("value rules validation failed: %v", err)
	}
}

func Test_MissingRequiredFields(t *testing.T) {
	// Start from valid payload and blank out fields one by one
	data := loadFixture(t, "issue_comment_trigger.json")
	var p handlers.IssueCommentPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	// comment.body
	q := p
	q.Comment.Body = ""
	if err := validateIssueCommentRequiredFields(q); err == nil {
		t.Fatalf("expected validation error for missing comment.body")
	}

	// issue.user.login
	q = p
	q.Issue.User.Login = ""
	if err := validateIssueCommentRequiredFields(q); err == nil {
		t.Fatalf("expected validation error for missing issue.user.login")
	}

	// repository.full_name
	q = p
	q.Repository.FullName = ""
	if err := validateIssueCommentRequiredFields(q); err == nil {
		t.Fatalf("expected validation error for missing repository.full_name")
	}
}

func Test_TypeMismatch_UnmarshalError(t *testing.T) {
	// issue.id should be number; provide string to force error
	invalid := []byte(`{"action":"created","issue":{"id":"oops","number":42,"state":"open","user":{"login":"octocat"}},"comment":{"id":1,"body":"/run-agent","user":{"login":"octocat"}},"repository":{"full_name":"octocat/hello-world","owner":{"login":"octocat"}}}`)
	var p handlers.IssueCommentPayload
	if err := json.Unmarshal(invalid, &p); err == nil {
		t.Fatalf("expected unmarshal error for type mismatch")
	}
}

func Test_ValueRules_InvalidCases(t *testing.T) {
	data := loadFixture(t, "issue_comment_trigger.json")
	var p handlers.IssueCommentPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	// action != created
	q := p
	q.Action = "edited"
	if err := validateIssueCommentValueRules(q); err == nil {
		t.Fatalf("expected validation error for action != created")
	}

	// state != open
	q = p
	q.Issue.State = "closed"
	if err := validateIssueCommentValueRules(q); err == nil {
		t.Fatalf("expected validation error for state != open")
	}

	// invalid full_name format
	q = p
	q.Repository.FullName = "octocat-hello-world"
	if err := validateIssueCommentValueRules(q); err == nil {
		t.Fatalf("expected validation error for invalid repository.full_name format")
	}
}

func Test_OptionalFields(t *testing.T) {
	data := loadFixture(t, "issue_comment_trigger.json")
	var p handlers.IssueCommentPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}

	// allow body = nil
	p.Issue.Body = nil
	if err := validateIssueCommentRequiredFields(p); err != nil {
		t.Fatalf("unexpected validation error with issue.body=nil: %v", err)
	}
}

func Test_EdgeCases(t *testing.T) {
	// Empty JSON object should fail required validation
	empty := []byte(`{}`)
	var p handlers.IssueCommentPayload
	if err := json.Unmarshal(empty, &p); err != nil {
		t.Fatalf("unexpected unmarshal error for empty object: %v", err)
	}
	if err := validateIssueCommentRequiredFields(p); err == nil {
		t.Fatalf("expected validation error for empty object")
	}

	// Invalid JSON should unmarshal with error
	invalid := loadFixture(t, "invalid.json")
	var p2 handlers.IssueCommentPayload
	if err := json.Unmarshal(invalid, &p2); err == nil {
		t.Fatalf("expected unmarshal error for invalid JSON")
	}
}
