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
// It calls the GitHub API to verify the user's permission level.
//
// Returns true if the user is a collaborator or has higher permissions (owner/admin),
// false if the user does not have collaborator permissions.
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
//   - bool: true if user has Collaborator+ permission, false otherwise
//   - error: GitHub API error (network error, rate limit, authentication error, etc.)
func (s *AuthorizationService) CheckPermission(ctx context.Context, owner, repo, username string) (bool, error) {
	s.logger.Info("Checking GitHub user permission",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("username", username),
	)

	hasPermission, err := s.githubClient.CheckCollaboratorPermission(ctx, owner, repo, username)
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

