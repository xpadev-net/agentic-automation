package repositories_test

import (
	"testing"

	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
)

// This test verifies that UpsertSelective does not clear existing metadata
// when only a subset of fields are provided.
func TestIssueRepository_UpsertSelective_PreservesMetadata(t *testing.T) {
	repo := repositories.NewIssueRepository()

	// Seed an existing issue with rich metadata
	body := "original body"
	issue := &models.Issue{
		Repo:          "o/r",
		Number:        101,
		GitHubIssueID: 999999,
		Title:         "original title",
		Body:          &body,
		Labels:        `{"labels":["a","b"]}`,
		State:         "open",
	}
	if err := repo.Create(issue); err != nil {
		t.Fatalf("seed create failed: %v", err)
	}

	// Apply selective upsert with only Title change
	updates := map[string]interface{}{
		"title": "updated title",
	}
	if err := repo.UpsertSelective("o/r", 101, updates); err != nil {
		t.Fatalf("UpsertSelective failed: %v", err)
	}

	// Reload and verify non-updated fields preserved
	got, err := repo.FindByRepoAndNumber("o/r", 101)
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if got.Title != "updated title" {
		t.Fatalf("title not updated, got=%q", got.Title)
	}
	if got.GitHubIssueID != 999999 {
		t.Fatalf("GitHubIssueID overwritten, got=%d", got.GitHubIssueID)
	}
	if got.Body == nil || *got.Body != "original body" {
		t.Fatalf("body overwritten, got=%v", got.Body)
	}
	if got.Labels != `{"labels":["a","b"]}` {
		t.Fatalf("labels overwritten, got=%s", got.Labels)
	}
	if got.State != "open" {
		t.Fatalf("state overwritten, got=%s", got.State)
	}
}
