package services_test

import (
	"context"
	"testing"

	appconfig "agentic-automation/internal/config"
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// fakeBuilder はテスト用の BlockerGraphBuilder。呼び出し回数を記録する。
type fakeBuilder struct{ called int }

func (f *fakeBuilder) BuildForIssue(ctx context.Context, owner, repo string, issueNumber int) error {
	f.called++
	return nil
}

func TestDependencyValidator_AllClosed_Allows(t *testing.T) {

	logger := zap.NewNop()
	// initialize in-memory sqlite and inject into config
	sdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	appconfig.SetDBForTesting(sdb)
	db := sdb
	// minimal schema
	if err := db.Exec(`
        CREATE TABLE IF NOT EXISTS issues (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            repo TEXT,
            number INTEGER,
            github_issue_id INTEGER,
            title TEXT,
            body TEXT,
            labels TEXT,
            state TEXT DEFAULT 'open',
            created_at DATETIME,
            updated_at DATETIME
        );
    `).Error; err != nil {
		t.Fatalf("create issues: %v", err)
	}
	if err := db.Exec(`
        CREATE TABLE IF NOT EXISTS blocker_graph_edges (
            task_id INTEGER,
            depends_on_task_id INTEGER,
            created_at DATETIME
        );
    `).Error; err != nil {
		t.Fatalf("create edges: %v", err)
	}

	issueRepo := repositories.NewIssueRepository()
	edgeRepo := repositories.NewBlockerGraphRepository()

	// Seed: root issue and one dependency(closed)
	root := &models.Issue{Repo: "o/r", Number: 1, Title: "root", State: "open"}
	if err := db.Create(root).Error; err != nil {
		t.Fatalf("seed root: %v", err)
	}
	dep := &models.Issue{Repo: "o/r", Number: 2, Title: "dep", State: "closed"}
	if err := db.Create(dep).Error; err != nil {
		t.Fatalf("seed dep: %v", err)
	}
	// edge: root depends on dep
	if err := edgeRepo.CreateEdge(root.ID, dep.ID); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	fb := &fakeBuilder{}
	v := services.NewDependencyValidator(fb, issueRepo, edgeRepo, logger)

	ctx := context.Background()
	res, err := v.ValidateUnblocked(ctx, "o", "r", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || len(res.BlockedDeps) != 0 {
		t.Fatalf("expected no blocked deps, got: %+v", res)
	}
	if fb.called == 0 {
		t.Fatalf("expected builder to be called")
	}
}

func TestDependencyValidator_HasOpen_Block(t *testing.T) {

	logger := zap.NewNop()
	sdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	appconfig.SetDBForTesting(sdb)
	db := sdb
	if err := db.Exec(`
        CREATE TABLE IF NOT EXISTS issues (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            repo TEXT,
            number INTEGER,
            github_issue_id INTEGER,
            title TEXT,
            body TEXT,
            labels TEXT,
            state TEXT DEFAULT 'open',
            created_at DATETIME,
            updated_at DATETIME
        );
    `).Error; err != nil {
		t.Fatalf("create issues: %v", err)
	}
	if err := db.Exec(`
        CREATE TABLE IF NOT EXISTS blocker_graph_edges (
            task_id INTEGER,
            depends_on_task_id INTEGER,
            created_at DATETIME
        );
    `).Error; err != nil {
		t.Fatalf("create edges: %v", err)
	}

	issueRepo := repositories.NewIssueRepository()
	edgeRepo := repositories.NewBlockerGraphRepository()

	// Seed: root issue and one dependency(open)
	root := &models.Issue{Repo: "a/b", Number: 10, Title: "root", State: "open"}
	if err := db.Create(root).Error; err != nil {
		t.Fatalf("seed root: %v", err)
	}
	dep := &models.Issue{Repo: "a/b", Number: 11, Title: "dep", State: "open"}
	if err := db.Create(dep).Error; err != nil {
		t.Fatalf("seed dep: %v", err)
	}
	// edge
	if err := edgeRepo.CreateEdge(root.ID, dep.ID); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	fb := &fakeBuilder{}
	v := services.NewDependencyValidator(fb, issueRepo, edgeRepo, logger)

	ctx := context.Background()
	res, err := v.ValidateUnblocked(ctx, "a", "b", 10)
	if err == nil {
		t.Fatalf("expected ErrBlockedDependencies, got nil")
	}
	if res == nil || len(res.BlockedDeps) != 1 {
		t.Fatalf("expected 1 blocked dep, got: %+v", res)
	}
}
