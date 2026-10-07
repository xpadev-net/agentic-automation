package webui

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
)

// kubernetesClientFactory is replaceable in tests.
var kubernetesClientFactory = clients.NewKubernetesClient

const (
	runsDefaultLimit = 50
	runsMaxLimit     = 200
	logsDefaultLimit = 500
	logsMaxLimit     = 1000
	sseKeepalive     = 15 * time.Second
	sseEndQuiet      = 10 * time.Second
	podLogTailLines  = 500
)

// runSummary is the JSON shape returned for a single agent run.
type runSummary struct {
	ID            int     `json:"id"`
	Repo          string  `json:"repo"`
	IssueNumber   int     `json:"issue_number"`
	IssueTitle    string  `json:"issue_title"`
	PRID          *int    `json:"pr_id,omitempty"`
	State         string  `json:"state"`
	AgentType     string  `json:"agent_type"`
	ExecutionMode string  `json:"execution_mode"`
	RetryCount    int     `json:"retry_count"`
	ErrorMessage  *string `json:"error_message,omitempty"`
	StartedAt     *string `json:"started_at,omitempty"`
	CompletedAt   *string `json:"completed_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

// runRow is the flat join of agent_runs with its issue.
type runRow struct {
	ID            int        `gorm:"column:id"`
	IssueID       int        `gorm:"column:issue_id"`
	PRID          *int       `gorm:"column:pr_id"`
	State         string     `gorm:"column:state"`
	AgentType     string     `gorm:"column:agent_type"`
	ExecutionMode string     `gorm:"column:execution_mode"`
	RetryCount    int        `gorm:"column:retry_count"`
	ErrorMessage  *string    `gorm:"column:error_message"`
	JobName       *string    `gorm:"column:job_name"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	StartedAt     *time.Time `gorm:"column:started_at"`
	CompletedAt   *time.Time `gorm:"column:completed_at"`
	IssueRepo     string     `gorm:"column:issue_repo"`
	IssueNumber   int        `gorm:"column:issue_number"`
	IssueTitle    string     `gorm:"column:issue_title"`
}

func (r *runRow) summary() runSummary {
	return runSummary{
		ID:            r.ID,
		Repo:          r.IssueRepo,
		IssueNumber:   r.IssueNumber,
		IssueTitle:    r.IssueTitle,
		PRID:          r.PRID,
		State:         r.State,
		AgentType:     r.AgentType,
		ExecutionMode: r.ExecutionMode,
		RetryCount:    r.RetryCount,
		ErrorMessage:  r.ErrorMessage,
		StartedAt:     rfc3339ptr(r.StartedAt),
		CompletedAt:   rfc3339ptr(r.CompletedAt),
		CreatedAt:     r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func rfc3339ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

const runJoinSelect = `agent_runs.id, agent_runs.issue_id, agent_runs.pr_id,
	agent_runs.state, agent_runs.agent_type, agent_runs.execution_mode,
	agent_runs.retry_count, agent_runs.error_message, agent_runs.job_name,
	agent_runs.created_at, agent_runs.started_at, agent_runs.completed_at,
	issues.repo AS issue_repo, issues.number AS issue_number, issues.title AS issue_title`

const runJoin = "LEFT JOIN issues ON issues.id = agent_runs.issue_id"

func (h *Handler) viewerCanSee(c *gin.Context, repo string) bool {
	if repo == "" {
		return false
	}
	token, err := h.GitHubTokenFrom(c)
	if err != nil {
		return false
	}
	login, _ := c.Get(ctxKeyLogin)
	loginStr, _ := login.(string)
	return h.perms.Allowed(c.Request.Context(), token, loginStr, repo, h.cfg.MinRepoPermission())
}

func clampLimit(v, def, max int) int {
	if v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// HandleListRuns lists recent agent runs the viewer may see.
func (h *Handler) HandleListRuns(c *gin.Context) {
	limit := clampLimit(atoi(c.Query("limit")), runsDefaultLimit, runsMaxLimit)
	offset := atoi(c.Query("offset"))
	// Fetch extra rows so permission filtering still fills the page.
	var rows []runRow
	q := h.db.Table("agent_runs").Select(runJoinSelect).Joins(runJoin).
		Order("agent_runs.id DESC").Offset(offset).Limit(limit * 3)
	if state := c.Query("state"); state != "" {
		q = q.Where("agent_runs.state = ?", state)
	}
	if err := q.Scan(&rows).Error; err != nil {
		h.logger.Error("Failed to list agent runs", config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	out := make([]runSummary, 0, limit)
	for i := range rows {
		if !h.viewerCanSee(c, rows[i].IssueRepo) {
			continue
		}
		out = append(out, rows[i].summary())
		if len(out) >= limit {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"runs": out, "limit": limit, "offset": offset})
}

// loadRun returns the joined run row or nil.
func (h *Handler) loadRun(runID int) (*runRow, error) {
	var rows []runRow
	err := h.db.Table("agent_runs").Select(runJoinSelect).Joins(runJoin).
		Where("agent_runs.id = ?", runID).Limit(1).Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// requireRunAccess loads the run and enforces repo visibility; on failure it
// writes the HTTP response and returns nil.
func (h *Handler) requireRunAccess(c *gin.Context) *runRow {
	runID := atoi(c.Param("id"))
	if runID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid run id"})
		return nil
	}
	run, err := h.loadRun(runID)
	if err != nil {
		h.logger.Error("Failed to load agent run", config.Int("run_id", runID), config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return nil
	}
	if run == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "agent run not found"})
		return nil
	}
	if !h.viewerCanSee(c, run.IssueRepo) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return nil
	}
	return run
}

// HandleGetRun returns a single run summary plus log metadata.
func (h *Handler) HandleGetRun(c *gin.Context) {
	run := h.requireRunAccess(c)
	if run == nil {
		return
	}
	count, err := h.logs.CountByAgentRun(run.ID)
	if err != nil {
		h.logger.Warn("Failed to count logs", config.Int("run_id", run.ID), config.Error(err))
	}
	resp := gin.H{"run": run.summary(), "logs_count": count}
	if run.JobName != nil {
		resp["job_name"] = *run.JobName
	}
	c.JSON(http.StatusOK, resp)
}

// HandleGetRunLogs returns persisted log entries ordered by seq. When the run
// has no pushed logs it falls back to the Kubernetes pod log (snapshot).
func (h *Handler) HandleGetRunLogs(c *gin.Context) {
	run := h.requireRunAccess(c)
	if run == nil {
		return
	}
	afterSeq := int64(atoi(c.Query("after_seq")))
	limit := clampLimit(atoi(c.Query("limit")), logsDefaultLimit, logsMaxLimit)

	entries, err := h.logs.GetAfterSeq(run.ID, afterSeq, limit+1)
	if err != nil {
		h.logger.Error("Failed to load agent run logs", config.Int("run_id", run.ID), config.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	if len(entries) == 0 && afterSeq == 0 {
		if podEntries := h.podLogSnapshot(c.Request.Context(), run); len(podEntries) > 0 {
			c.JSON(http.StatusOK, gin.H{"entries": podEntries, "source": "pod", "has_more": false})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "source": "db", "has_more": hasMore})
}

// podLogSnapshot fetches the last lines of the run's pod log as synthetic
// entries (seq 1..n). Returns nil when unavailable.
func (h *Handler) podLogSnapshot(ctx context.Context, run *runRow) []gin.H {
	kc, err := kubernetesClientFactory(h.logger)
	if err != nil {
		return nil
	}
	var pods *corev1.PodList
	if run.JobName != nil && *run.JobName != "" {
		pods, err = kc.ListPods(ctx, "job-name="+*run.JobName)
	} else {
		pods, err = kc.ListPods(ctx, fmt.Sprintf("agent-run-id=%d", run.ID))
	}
	if err != nil || len(pods.Items) == 0 {
		return nil
	}
	tail := int64(podLogTailLines)
	text, err := kc.GetPodLogs(ctx, pods.Items[0].Name, &tail)
	if err != nil || text == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	entries := make([]gin.H, 0, len(lines))
	for i, l := range lines {
		entries = append(entries, gin.H{"seq": i + 1, "line": l})
	}
	return entries
}
