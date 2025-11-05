package services

import (
	"agentic-automation/internal/models"
	"agentic-automation/internal/repositories"
	"agentic-automation/internal/services"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDBForCIStatusProvider(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	db.Exec(`CREATE TABLE pull_requests (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        repo TEXT,
        number INTEGER,
        issue_id INTEGER,
        branch TEXT,
        base_branch TEXT,
        status TEXT,
        mergeable BOOLEAN,
        created_at DATETIME,
        updated_at DATETIME,
        UNIQUE(repo, number)
    )`)
	db.Exec(`CREATE TABLE ci_status (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        pr_id INTEGER,
        check_suite_id TEXT,
        check_run_id TEXT,
        name TEXT,
        status TEXT,
        conclusion TEXT,
        logs TEXT,
        logs_url TEXT,
        started_at DATETIME,
        completed_at DATETIME,
        created_at DATETIME,
        updated_at DATETIME,
        UNIQUE(check_suite_id, pr_id)
    )`)
	return db
}

func TestCIStatusProvider_Mapping_Success(t *testing.T) {
	db := setupTestDBForCIStatusProvider(t)
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepositoryWithDB(db)

	pr := &models.PullRequest{Repo: "o/r", Number: 1, Status: "open"}
	require.NoError(t, db.Create(pr).Error)

	now := time.Now()
	conclusion := "success"
	st := &models.CIStatus{PRID: pr.ID, CheckSuiteID: "1", Name: "aggregated", Status: "completed", Conclusion: &conclusion, CompletedAt: &now}
	require.NoError(t, ciRepo.CreateOrUpdate(st))

	prov := services.NewCIStatusProvider(ciRepo, prRepo, nil)
	state, err := prov.GetAggregatedState(nil, "o", "r", 1)
	require.NoError(t, err)
	require.Equal(t, services.CIStateSuccess, state)
}

func TestCIStatusProvider_Mapping_FailedAndPending(t *testing.T) {
	db := setupTestDBForCIStatusProvider(t)
	prRepo := repositories.NewPullRequestRepository(db)
	ciRepo := repositories.NewCIStatusRepositoryWithDB(db)

	pr := &models.PullRequest{Repo: "o/r2", Number: 2, Status: "open"}
	require.NoError(t, db.Create(pr).Error)

	// failed
	now := time.Now()
	failure := "failure"
	stFailed := &models.CIStatus{PRID: pr.ID, CheckSuiteID: "2", Name: "aggregated", Status: "completed", Conclusion: &failure, CompletedAt: &now}
	require.NoError(t, ciRepo.CreateOrUpdate(stFailed))
	prov := services.NewCIStatusProvider(ciRepo, prRepo, nil)
	state, err := prov.GetAggregatedState(nil, "o", "r2", 2)
	require.NoError(t, err)
	require.Equal(t, services.CIStateFailed, state)

	// pending (no aggregated rows)
	pr3 := &models.PullRequest{Repo: "o/r3", Number: 3, Status: "open"}
	require.NoError(t, db.Create(pr3).Error)
	state2, err := prov.GetAggregatedState(nil, "o", "r3", 3)
	require.NoError(t, err)
	require.Equal(t, services.CIStatePending, state2)
}
