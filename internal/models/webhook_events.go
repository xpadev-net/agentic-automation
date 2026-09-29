package models

// Package models provides data models and type definitions for the GitHub Agent Automation system.
// This file contains constants and helper functions for GitHub webhook event types and actions.

// Webhook event types supported by the system
const (
	EventTypeIssueComment             = "issue_comment"
	EventTypeIssues                   = "issues"
	EventTypePullRequest              = "pull_request"
	EventTypePullRequestReview        = "pull_request_review"
	EventTypePullRequestReviewComment = "pull_request_review_comment"
	EventTypeCheckSuite               = "check_suite"
	EventTypeStatus                   = "status"
	EventTypeWorkflowRun              = "workflow_run"
)

// Webhook action values for each event type
const (
	// issue_comment actions
	IssueCommentActionCreated = "created"

	// issues actions
	IssuesActionAssigned = "assigned"
	IssuesActionClosed   = "closed"
	IssuesActionReopened = "reopened"

	// pull_request actions
	PullRequestActionOpened      = "opened"
	PullRequestActionSynchronize = "synchronize"
	PullRequestActionClosed      = "closed"

	// pull_request_review actions
	PullRequestReviewActionSubmitted = "submitted"

	// pull_request_review_comment actions
	PullRequestReviewCommentActionCreated = "created"

	// check_suite actions
	CheckSuiteActionCompleted = "completed"

	// workflow_run actions
	WorkflowRunActionCompleted = "completed"
)

// check_suite conclusion values
const (
	CheckSuiteConclusionSuccess   = "success"
	CheckSuiteConclusionFailure   = "failure"
	CheckSuiteConclusionCancelled = "cancelled"
	CheckSuiteConclusionSkipped   = "skipped"
	CheckSuiteConclusionNeutral   = "neutral"
)

// status event state values
const (
	StatusStatePending = "pending"
	StatusStateSuccess = "success"
	StatusStateFailure = "failure"
	StatusStateError   = "error"
)

// IsValidEventType checks if the provided event type is a valid/supported webhook event type.
// It returns true if eventType matches one of the defined event type constants.
//
// Parameters:
//   - eventType: The event type string to validate (e.g., "issue_comment", "pull_request")
//
// Returns:
//   - bool: true if the event type is valid, false otherwise
func IsValidEventType(eventType string) bool {
	switch eventType {
	case EventTypeIssueComment, EventTypeIssues, EventTypePullRequest,
		EventTypePullRequestReview, EventTypePullRequestReviewComment,
		EventTypeCheckSuite, EventTypeStatus, EventTypeWorkflowRun:
		return true
	default:
		return false
	}
}

// IsValidAction checks if the provided action is valid for the given event type.
// It validates that the action value is allowed for the specified event type.
//
// Parameters:
//   - eventType: The event type string (e.g., "issue_comment", "pull_request")
//   - action: The action string to validate (e.g., "created", "opened")
//
// Returns:
//   - bool: true if the action is valid for the event type, false otherwise.
//     For status events, always returns true as status events don't have an action field.
func IsValidAction(eventType, action string) bool {
	switch eventType {
	case EventTypeIssueComment:
		return action == IssueCommentActionCreated
	case EventTypeIssues:
		return action == IssuesActionAssigned || action == IssuesActionClosed || action == IssuesActionReopened
	case EventTypePullRequest:
		return action == PullRequestActionOpened ||
			action == PullRequestActionSynchronize ||
			action == PullRequestActionClosed
	case EventTypePullRequestReview:
		return action == PullRequestReviewActionSubmitted
	case EventTypePullRequestReviewComment:
		return action == PullRequestReviewCommentActionCreated
	case EventTypeCheckSuite:
		return action == CheckSuiteActionCompleted
	case EventTypeWorkflowRun:
		return action == WorkflowRunActionCompleted
	case EventTypeStatus:
		// status events don't have an action field, so always return true
		return true
	default:
		return false
	}
}

// GetSupportedEventTypes returns a slice of all supported webhook event types.
// The event types are returned in the order they are defined.
//
// Returns:
//   - []string: A slice containing all supported event type constants
func GetSupportedEventTypes() []string {
	return []string{
		EventTypeIssueComment,
		EventTypeIssues,
		EventTypePullRequest,
		EventTypePullRequestReview,
		EventTypePullRequestReviewComment,
		EventTypeCheckSuite,
		EventTypeStatus,
		EventTypeWorkflowRun,
	}
}
