package services

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"strings"
	"time"

	"agentic-automation/internal/clients"
	"agentic-automation/internal/config"
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
	logger       *config.AppLogger
}

// NewIssueContextService creates a new IssueContextService instance.
// It requires a GitHub client and logger as dependencies.
//
// Parameters:
//   - githubClient: GitHub API client (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses config.NewNopLogger())
//
// Returns:
//   - *IssueContextService: Initialized service instance
func NewIssueContextService(githubClient *clients.Client, logger *config.AppLogger) *IssueContextService {
	if githubClient == nil {
		panic("githubClient is required for IssueContextService")
	}

	// Use config.NewNopLogger() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = config.NewNopLogger()
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
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
	)

	// Fetch Issue details
	s.logger.Debug("Fetching Issue details from GitHub API",
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
	)

	issue, err := s.githubClient.GetIssue(ctx, owner, repo, issueNumber)
	if err != nil {
		s.logger.Error("Failed to fetch Issue from GitHub API",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
			config.Error(err),
		)
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	if issue == nil {
		s.logger.Error("GitHub API returned nil issue",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
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
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
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
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
		)
	}

	// Fetch Issue comments
	s.logger.Debug("Fetching Issue comments from GitHub API",
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
	)

	comments, err := s.githubClient.ListIssueComments(ctx, owner, repo, issueNumber)
	if err != nil {
		s.logger.Error("Failed to fetch Issue comments from GitHub API",
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
			config.Error(err),
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
			config.String("owner", owner),
			config.String("repo", repo),
			config.Int("issue_number", issueNumber),
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
		config.String("owner", owner),
		config.String("repo", repo),
		config.Int("issue_number", issueNumber),
		config.Int("comments_count", len(convertedComments)),
		config.Int("labels_count", len(labels)),
		config.Bool("has_body", body != ""),
	)

	return issueCtx, nil
}

// FormatPrompt formats the IssueContext into an XML-like string prompt that can be passed
// to the agent-runner via the --prompt command-line argument.
//
// The format uses XML-like structure with CDATA sections for text content:
//   - Root element: <issue_context>
//   - Issue information with number attribute and nested title/description
//   - User Instruction section (if provided, wrapped in CDATA)
//   - Previous Conversation section with comment elements (user and created_at attributes)
//   - Labels section with individual label elements
//
// Text content (description, user_instruction, comment body) is wrapped in CDATA sections
// to avoid XML escaping issues.
//
// Parameters:
//   - issueCtx: IssueContext to format (must not be nil)
//   - userInstruction: Optional user instruction from comment (e.g., "/run-agent <instruction>")
//
// Returns:
//   - string: Formatted XML-like prompt string ready for agent-runner
func (s *IssueContextService) FormatPrompt(issueCtx *IssueContext, userInstruction string) string {
	if issueCtx == nil {
		s.logger.Warn("FormatPrompt called with nil IssueContext, returning empty string")
		return ""
	}

	var builder strings.Builder

	// Root element
	builder.WriteString("<issue_context>\n")

	// Issue element with number attribute
	builder.WriteString(fmt.Sprintf("  <issue number=\"%d\">\n", issueCtx.Number))

	// Title (escape XML special characters)
	builder.WriteString("    <title>")
	if issueCtx.Title != "" {
		_ = xml.EscapeText(&builder, []byte(issueCtx.Title))
	}
	builder.WriteString("</title>\n")

	// Description with CDATA
	builder.WriteString("    <description>")
	if issueCtx.Body != "" {
		builder.WriteString(fmt.Sprintf("<![CDATA[%s]]>", issueCtx.Body))
	}
	builder.WriteString("</description>\n")

	builder.WriteString("  </issue>\n")

	// User Instruction section (if provided)
	if userInstruction != "" {
		builder.WriteString("  <user_instruction><![CDATA[")
		builder.WriteString(userInstruction)
		builder.WriteString("]]></user_instruction>\n")
	}

	// Previous Conversation section
	builder.WriteString("  <previous_conversation>\n")
	if len(issueCtx.Comments) == 0 {
		builder.WriteString("  </previous_conversation>\n")
	} else {
		for _, comment := range issueCtx.Comments {
			user := comment.User
			if user == "" {
				user = "(unknown)"
			}
			// Escape XML attribute value (user)
			escapedUser := html.EscapeString(user)

			// Format created_at as ISO 8601 (RFC3339)
			createdAtStr := ""
			if !comment.CreatedAt.IsZero() {
				createdAtStr = comment.CreatedAt.Format(time.RFC3339)
			}

			builder.WriteString("    <comment")
			builder.WriteString(fmt.Sprintf(" user=\"%s\"", escapedUser))
			if createdAtStr != "" {
				builder.WriteString(fmt.Sprintf(" created_at=\"%s\"", createdAtStr))
			}
			builder.WriteString(">\n")

			// Comment body with CDATA
			if comment.Body != "" {
				builder.WriteString(fmt.Sprintf("      <![CDATA[%s]]>\n", comment.Body))
			}

			builder.WriteString("    </comment>\n")
		}
		builder.WriteString("  </previous_conversation>\n")
	}

	// Labels section
	builder.WriteString("  <labels>\n")
	if len(issueCtx.Labels) == 0 {
		builder.WriteString("  </labels>\n")
	} else {
		for _, label := range issueCtx.Labels {
			// Escape XML special characters in label
			builder.WriteString("    <label>")
			_ = xml.EscapeText(&builder, []byte(label))
			builder.WriteString("</label>\n")
		}
		builder.WriteString("  </labels>\n")
	}

	builder.WriteString("</issue_context>\n")

	return builder.String()
}
