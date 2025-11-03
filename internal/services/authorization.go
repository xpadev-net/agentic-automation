package services

import (
	"context"

	"agentic-automation/internal/clients"
	"go.uber.org/zap"
)

// Package services provides business logic services for the GitHub Agent Automation system.
// AuthorizationService handles GitHub repository permission checks.

// AuthorizationService provides methods to check GitHub repository permissions.
// It wraps the GitHub API client to provide a service-layer abstraction
// for authorization checks required by webhook handlers.
type AuthorizationService struct {
	githubClient *clients.Client
	logger       *zap.Logger
}

// NewAuthorizationService creates a new AuthorizationService instance.
// It requires a GitHub client and logger as dependencies.
//
// Parameters:
//   - githubClient: GitHub API client (must not be nil, will panic if nil)
//   - logger: Structured logger instance (if nil, uses zap.NewNop())
//
// Returns:
//   - *AuthorizationService: Initialized service instance
func NewAuthorizationService(githubClient *clients.Client, logger *zap.Logger) *AuthorizationService {
	if githubClient == nil {
		panic("githubClient is required for AuthorizationService")
	}

	// Use zap.NewNop() if logger is nil to prevent nil pointer dereference
	if logger == nil {
		logger = zap.NewNop()
	}

	return &AuthorizationService{
		githubClient: githubClient,
		logger:       logger,
	}
}

// CheckPermission checks if a GitHub user has Collaborator+ permission on a repository.
// This includes repository owners, explicit collaborators, and organization team members
// with write/admin access. It uses the GitHub API GetPermissionLevel endpoint to accurately
// determine the user's permission level (admin, write, read, none).
//
// This method implements FR-018 requirement: "認可は「リポジトリのCollaborator以上」のユーザーのみに限定する".
// It returns true for users with admin or write permission, which includes:
//   - Repository owners
//   - Explicit collaborators with write/admin access
//   - Organization team members with write/admin access
//
// Returns true if the user has admin or write permission, false if the user has read
// permission, no permission, or the user/repository does not exist.
// Returns an error if the GitHub API call fails (network error, rate limit, etc.).
//
// Note: 404 responses from GitHub API are treated as "no permission" (false, nil),
// as handled by the underlying GitHub client.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - owner: Repository owner (e.g., "octocat")
//   - repo: Repository name (e.g., "hello-world")
//   - username: GitHub username to check (e.g., "octocat")
//
// Returns:
//   - bool: true if user has Collaborator+ permission (admin/write), false otherwise
//   - error: GitHub API error (network error, rate limit, authentication error, etc.)
func (s *AuthorizationService) CheckPermission(ctx context.Context, owner, repo, username string) (bool, error) {
	s.logger.Info("Checking GitHub user permission",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("username", username),
	)

	hasPermission, err := s.githubClient.CheckWritePermission(ctx, owner, repo, username)
	if err != nil {
		s.logger.Error("GitHub user permission check failed",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.String("username", username),
			zap.Error(err),
		)
		return false, err
	}

	s.logger.Info("GitHub user permission check completed",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("username", username),
		zap.Bool("has_permission", hasPermission),
	)

	return hasPermission, nil
}

