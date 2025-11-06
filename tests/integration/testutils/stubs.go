package testutils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/models"
	"agentic-automation/internal/services"

	"github.com/google/go-github/v76/github"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StubAuthorization implements handlers.Authorization
type StubAuthorization struct {
	Allow bool
	Err   error
}

func (s *StubAuthorization) CheckPermission(ctx context.Context, owner, repo, username string) (bool, error) {
	return s.Allow, s.Err
}

// StubIssueContext implements handlers.IssueContext
type StubIssueContext struct {
	Context *services.IssueContext
}

func (s *StubIssueContext) CollectIssueContext(ctx context.Context, owner, repo string, issueNumber int) (*services.IssueContext, error) {
	return s.Context, nil
}

func (s *StubIssueContext) FormatPrompt(issueCtx *services.IssueContext, userInstruction string) string {
	if issueCtx == nil {
		return ""
	}
	// Minimal prompt sufficient for assertions
	return "Issue #" + string(rune(issueCtx.Number))
}

// StubGitHubNotification implements handlers.GitHubNotification
type StubGitHubNotification struct {
	Called bool
	Err    error
}

func (s *StubGitHubNotification) PostExecutionStartComment(ctx context.Context, owner, repo string, issueNumber int, agentType string, agentRunID int) error {
	s.Called = true
	return s.Err
}

// NotifyDependencyViolation satisfies handlers.GitHubNotification for integration tests
func (s *StubGitHubNotification) NotifyDependencyViolation(ctx context.Context, owner, repo string, issueNumber, prNumber int, blocked []models.Issue, idempotencyKey string) error {
	s.Called = true
	return s.Err
}

// CreatedJobInfo represents information about a Job created by StubKubernetesJobService
type CreatedJobInfo struct {
	AgentRunID int
	RetryCount int
	Prompt     string
	Feedback   *services.AggregatedFeedback
	JobName    string
}

// StubKubernetesJobService implements services.KubernetesJobService interface for testing
type StubKubernetesJobService struct {
	CreatedJobs     []CreatedJobInfo
	Error           error
	CreateJobCalled bool
}

// CreateJobForAgentRun creates a Kubernetes Job for the given AgentRun and Issue
func (s *StubKubernetesJobService) CreateJobForAgentRun(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, branchName string) (*batchv1.Job, error) {
	s.CreateJobCalled = true

	if s.Error != nil {
		return nil, s.Error
	}

	jobName := fmt.Sprintf("agent-runner-%d", agentRun.ID)

	info := CreatedJobInfo{
		AgentRunID: agentRun.ID,
		RetryCount: agentRun.RetryCount,
		Prompt:     prompt,
		Feedback:   nil,
		JobName:    jobName,
	}
	s.CreatedJobs = append(s.CreatedJobs, info)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: "default",
			Labels: map[string]string{
				"app":          "agent-runner",
				"agent-run-id": strconv.Itoa(agentRun.ID),
				"issue-id":     strconv.Itoa(issue.Number),
			},
		},
	}

	return job, nil
}

// CreateJobForAgentRunWithFeedback creates a Kubernetes Job with aggregated feedback for retry
func (s *StubKubernetesJobService) CreateJobForAgentRunWithFeedback(ctx context.Context, agentRun *models.AgentRun, issue *models.Issue, prompt string, feedback *services.AggregatedFeedback, branchName string) (*batchv1.Job, error) {
	s.CreateJobCalled = true

	if s.Error != nil {
		return nil, s.Error
	}

	jobName := fmt.Sprintf("agent-runner-%d", agentRun.ID)

	info := CreatedJobInfo{
		AgentRunID: agentRun.ID,
		RetryCount: agentRun.RetryCount,
		Prompt:     prompt,
		Feedback:   feedback,
		JobName:    jobName,
	}
	s.CreatedJobs = append(s.CreatedJobs, info)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: "default",
			Labels: map[string]string{
				"app":          "agent-runner",
				"agent-run-id": strconv.Itoa(agentRun.ID),
				"issue-id":     strconv.Itoa(issue.Number),
			},
		},
	}

	return job, nil
}

// StubCIFailureAnalyzer implements CI failure analysis for testing
type StubCIFailureAnalyzer struct {
	Result                 *services.CIFailureResult
	Error                  error
	Called                 bool
	CalledWithCheckSuiteID int64
}

// AnalyzeCIFailure analyzes CI failure logs from a check suite
func (s *StubCIFailureAnalyzer) AnalyzeCIFailure(ctx context.Context, owner string, repo string, checkSuiteID int64) (*services.CIFailureResult, error) {
	s.Called = true
	s.CalledWithCheckSuiteID = checkSuiteID

	if s.Error != nil {
		return nil, s.Error
	}

	if s.Result != nil {
		return s.Result, nil
	}

	// Default result if Result is nil
	return &services.CIFailureResult{
		Summary:          "Test failure detected",
		Excerpt:          "Error at line 42",
		FailureType:      "test_failure",
		FailedCheckNames: []string{"test-suite"},
	}, nil
}

// --- Dummy GitHub client for IssueContextService/CIFailureAnalyzer ---
// staticTransport returns a fixed JSON body with 200 OK for any request
type staticTransport struct{}

func (t *staticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	path := r.URL.Path
	var body string
	switch {
	case strings.Contains(path, "/issues/") && strings.HasSuffix(path, "/comments"):
		// Issue comments list
		body = `[]`
	case strings.Contains(path, "/repos/") && strings.Contains(path, "/issues/"):
		// Issue detail
		body = `{"number":123,"title":"Test","body":"Body","labels":[]}`
	case strings.Contains(path, "/check-suites/") && strings.HasSuffix(path, "/check-runs"):
		// List check runs for check suite
		body = `{"total_count":1,"check_runs":[{"id":1,"name":"test-suite","conclusion":"failure","output":{"text":"Test failed"}}]}`
	default:
		body = `{"id":1}`
	}
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

// NewDummyGitHubHTTPClient returns an *http.Client suitable for wrapping in a github.Client
func NewDummyGitHubHTTPClient() *http.Client {
	return &http.Client{Transport: &staticTransport{}}
}

// NewDummyGitHubClient returns an internal *clients.Client backed by a github.Client
// using the static transport so code paths can run deterministically in tests.
func NewDummyGitHubClient() *clients.Client {
	httpClient := NewDummyGitHubHTTPClient()
	gh := github.NewClient(httpClient)
	return clients.NewFromGitHub(gh, zap.NewNop())
}
