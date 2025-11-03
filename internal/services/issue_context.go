package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentic-automation/internal/clients"
	"go.uber.org/zap"
)

// IssueContext represents collected information about a GitHub Issue
// including its body, comments, and labels for use by the agent-runner.
type IssueContext struct {
	Number   int
	Title    string
	Body     string
	Comments []Comment
	Labels   []string
}

// Comment represents a single comment on an Issue
type Comment struct {
	Body      string
	User      string
	CreatedAt time.Time
}

// IssueContextService provides methods to collect and format Issue context from GitHub.
// It wraps the GitHub API client to fetch Issue details, comments, and labels,
// then formats them for use by the agent-runner.
type IssueContextService struct {
	githubClient *clients.Client
	logger       *zap.Logger
}

// NewIssueContextService creates a new IssueContextService instance.
// It requires a GitHub client and logger as dependencies.
//
// Parameters:
//   - githubClient: GitHub API client (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *IssueContextService: Initialized service instance
func NewIssueContextService(githubClient *clients.Client, logger *zap.Logger) *IssueContextService {
	if githubClient == nil {
		panic("githubClient is required for IssueContextService")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &IssueContextService{
		githubClient: githubClient,
		logger:       logger,
	}
}

// CollectIssueContext retrieves Issue information from GitHub API including
// the Issue body, all comments, and labels.
//
// This method fetches:
//   - Issue details (title, body, number, labels) via GetIssue()
//   - All comments via ListIssueComments() (handles pagination automatically)
//
// Returns an error if the GitHub API calls fail (network error, 404, rate limit, etc.).
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - issueNumber: GitHub Issue number
//
// Returns:
//   - *IssueContext: Collected Issue information, or nil on error
//   - error: GitHub API error if any step fails
func (s *IssueContextService) CollectIssueContext(ctx context.Context, owner, repo string, issueNumber int) (*IssueContext, error) {
	s.logger.Info("Collecting Issue context",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	// Fetch Issue details
	s.logger.Debug("Fetching Issue details from GitHub API",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	issue, err := s.githubClient.GetIssue(ctx, owner, repo, issueNumber)
	if err != nil {
		s.logger.Error("Failed to fetch Issue from GitHub API",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	if issue == nil {
		s.logger.Error("GitHub API returned nil issue",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
		)
		return nil, fmt.Errorf("issue not found: issue number %d", issueNumber)
	}

	// Extract Issue fields with safe nil handling
	title := ""
	if issue.Title != nil {
		title = *issue.Title
	}

	body := ""
	if issue.Body != nil {
		body = *issue.Body
	}

	number := issue.GetNumber()
	if number == 0 {
		s.logger.Error("Issue number is zero",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
		)
		return nil, fmt.Errorf("invalid issue number: %d", number)
	}

	// Extract labels
	labels := make([]string, 0)
	if issue.Labels != nil && len(issue.Labels) > 0 {
		labels = make([]string, 0, len(issue.Labels))
		for _, label := range issue.Labels {
			if label != nil && label.Name != nil {
				labels = append(labels, *label.Name)
			}
		}
	}

	// Log warnings for empty data
	if body == "" {
		s.logger.Warn("Issue body is empty",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
		)
	}

	// Fetch Issue comments
	s.logger.Debug("Fetching Issue comments from GitHub API",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
	)

	comments, err := s.githubClient.ListIssueComments(ctx, owner, repo, issueNumber)
	if err != nil {
		s.logger.Error("Failed to fetch Issue comments from GitHub API",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to list issue comments: %w", err)
	}

	// Convert []*github.IssueComment to []Comment
	convertedComments := make([]Comment, 0)
	if comments != nil && len(comments) > 0 {
		convertedComments = make([]Comment, 0, len(comments))
		for _, comment := range comments {
			if comment == nil {
				continue
			}

			commentBody := ""
			if comment.Body != nil {
				commentBody = *comment.Body
			}

			user := ""
			if comment.User != nil && comment.User.Login != nil {
				user = *comment.User.Login
			}

			createdAt := time.Time{}
			if comment.CreatedAt != nil {
				createdAt = comment.CreatedAt.Time
			}

			convertedComments = append(convertedComments, Comment{
				Body:      commentBody,
				User:      user,
				CreatedAt: createdAt,
			})
		}
	}

	if len(convertedComments) == 0 {
		s.logger.Warn("No comments found for Issue",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("issue_number", issueNumber),
		)
	}

	// Build IssueContext
	issueCtx := &IssueContext{
		Number:   number,
		Title:    title,
		Body:     body,
		Comments: convertedComments,
		Labels:   labels,
	}

	s.logger.Info("Issue context collected successfully",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.Int("issue_number", issueNumber),
		zap.Int("comments_count", len(convertedComments)),
		zap.Int("labels_count", len(labels)),
		zap.Bool("has_body", body != ""),
	)

	return issueCtx, nil
}

// FormatPrompt formats the IssueContext into a string prompt that can be passed
// to the agent-runner via the --prompt command-line argument.
//
// The format follows the structure defined in contracts/ai-agent-execution.md:
//   - Issue header: "Issue #<number>: <title>"
//   - Description section with Issue body
//   - Comments section with all comments (or "(none)" if empty)
//   - Labels section with comma-separated labels (or "(none)" if empty)
//
// Parameters:
//   - issueCtx: IssueContext to format (must not be nil)
//
// Returns:
//   - string: Formatted prompt string ready for agent-runner
func (s *IssueContextService) FormatPrompt(issueCtx *IssueContext) string {
	if issueCtx == nil {
		s.logger.Warn("FormatPrompt called with nil IssueContext, returning empty string")
		return ""
	}

	var builder strings.Builder

	// Issue header
	builder.WriteString(fmt.Sprintf("Issue #%d: %s\n\n", issueCtx.Number, issueCtx.Title))

	// Description section
	builder.WriteString("Description:\n")
	if issueCtx.Body != "" {
		builder.WriteString(issueCtx.Body)
	} else {
		builder.WriteString("(none)")
	}
	builder.WriteString("\n\n")

	// Comments section
	builder.WriteString("Comments:\n")
	if len(issueCtx.Comments) == 0 {
		builder.WriteString("(none)\n")
	} else {
		for _, comment := range issueCtx.Comments {
			// Format: "- <user>: <body>"
			user := comment.User
			if user == "" {
				user = "(unknown)"
			}
			builder.WriteString(fmt.Sprintf("- %s: %s\n", user, comment.Body))
		}
	}
	builder.WriteString("\n")

	// Labels section
	builder.WriteString("Labels: ")
	if len(issueCtx.Labels) == 0 {
		builder.WriteString("(none)")
	} else {
		builder.WriteString(strings.Join(issueCtx.Labels, ", "))
	}
	builder.WriteString("\n")

	return builder.String()
}
