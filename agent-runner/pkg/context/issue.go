package context

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Issue represents the GitHub Issue context passed to the agent
type Issue struct {
	ID               int
	Title            string // promptから抽出（後続タスクで実装）
	Body             string // promptから抽出（後続タスクで実装）
	Repo             string // owner/repo format
	Labels           []string
	PreviousAttempts []PreviousAttempt
	CILogs           string
}

// PreviousAttempt represents a previous retry attempt
type PreviousAttempt struct {
	RetryCount int    `json:"retry_count"`
	Error      string `json:"error"`
	CILogs     string `json:"ci_logs,omitempty"`
}

// ParseError represents a parsing error with field context
type ParseError struct {
	Field   string
	Message string
	Err     error // wrapped error for Unwrap()
}

// Error implements the error interface
func (e *ParseError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("invalid %s: %s: %v", e.Field, e.Message, e.Err)
	}
	return fmt.Sprintf("invalid %s: %s", e.Field, e.Message)
}

// Unwrap returns the underlying error if any
func (e *ParseError) Unwrap() error {
	return e.Err
}

// repoPattern matches owner/repo format
var repoPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)

// validateIssueID validates that issueID is a positive integer
func validateIssueID(issueID int) error {
	if issueID <= 0 {
		return &ParseError{
			Field:   "issue-id",
			Message: fmt.Sprintf("must be positive integer, got: %d", issueID),
		}
	}
	return nil
}

// validateRepo validates that repo is in owner/repo format
func validateRepo(repo string) error {
	if !repoPattern.MatchString(repo) {
		return &ParseError{
			Field:   "repo",
			Message: fmt.Sprintf("must be in format owner/repo, got: %q", repo),
		}
	}
	return nil
}

// validatePrompt validates that prompt is not empty
func validatePrompt(prompt string) error {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return &ParseError{
			Field:   "prompt",
			Message: "must not be empty",
		}
	}
	return nil
}

// parsePreviousAttempts parses JSON string into []PreviousAttempt
// Returns nil slice and nil error if jsonStr is empty
func parsePreviousAttempts(jsonStr string) ([]PreviousAttempt, error) {
	if jsonStr == "" {
		return nil, nil
	}

	// First check if it's null
	var rawValue interface{}
	if err := json.Unmarshal([]byte(jsonStr), &rawValue); err != nil {
		return nil, &ParseError{
			Field:   "previous-attempts",
			Message: "invalid JSON",
			Err:     err,
		}
	}

	// Check if it's null
	if rawValue == nil {
		return nil, &ParseError{
			Field:   "previous-attempts",
			Message: "must be an array, got null",
		}
	}

	// Check if it's an array
	if _, ok := rawValue.([]interface{}); !ok {
		return nil, &ParseError{
			Field:   "previous-attempts",
			Message: fmt.Sprintf("must be an array, got %T", rawValue),
		}
	}

	// Now parse into []PreviousAttempt
	var attempts []PreviousAttempt
	if err := json.Unmarshal([]byte(jsonStr), &attempts); err != nil {
		return nil, &ParseError{
			Field:   "previous-attempts",
			Message: "invalid JSON",
			Err:     err,
		}
	}

	// Validate each attempt
	for i, attempt := range attempts {
		if attempt.RetryCount < 0 {
			return nil, &ParseError{
				Field:   "previous-attempts",
				Message: fmt.Sprintf("attempt[%d].retry_count must be non-negative, got: %d", i, attempt.RetryCount),
			}
		}
		// Require at least retry_count or error to be set (not both zero/empty)
		if attempt.RetryCount == 0 && attempt.Error == "" {
			return nil, &ParseError{
				Field:   "previous-attempts",
				Message: fmt.Sprintf("attempt[%d] must have at least retry_count or error field", i),
			}
		}
	}

	return attempts, nil
}

// ParseIssueContext parses Issue context from command-line arguments
func ParseIssueContext(issueID int, repo, prompt, previousAttemptsJSON, ciLogs string) (*Issue, error) {
	// Validate issueID
	if err := validateIssueID(issueID); err != nil {
		return nil, err
	}

	// Validate repo
	if err := validateRepo(repo); err != nil {
		return nil, err
	}

	// Validate prompt
	if err := validatePrompt(prompt); err != nil {
		return nil, err
	}

	// Parse previous attempts
	previousAttempts, err := parsePreviousAttempts(previousAttemptsJSON)
	if err != nil {
		return nil, err
	}

	// Build Issue struct
	issue := &Issue{
		ID:               issueID,
		Title:            "", // TODO: extract from prompt in future task
		Body:             prompt,
		Repo:             repo,
		Labels:           []string{}, // TODO: populate in future task
		PreviousAttempts: previousAttempts,
		CILogs:           ciLogs,
	}

	return issue, nil
}

// BuildPlanCreationPrompt constructs the prompt for plan creation mode.
func BuildPlanCreationPrompt(reviewFeedback string) string {
	return fmt.Sprintf(`以下のレビューフィードバックを分析し、対応プランを作成してください。

レビューフィードバック:
%s

## 必須要件 (MUST)

1. 指摘事項を整理し、対応が必要な項目を特定すること
2. 各項目に対する具体的な対応方法を提案すること
3. 対応が困難または不要な場合は、却下理由を明確にすること
4. プランは回答として直接出力すること
5. 以下の形式で出力すること:
   - プランを作成する場合: <plan_created>[プラン内容]</plan_created>
   - プランを却下する場合: <plan_rejected>[却下理由]</plan_rejected>

## 禁止事項 (MUST NOT)

1. ファイルに書き出してはならない
2. ファイルの変更を行ってはならない
3. ファイルの作成、編集、削除を行ってはならない

## 補足

- タグの外側に説明文やその他の文章が含まれていても構いません。タグ内の内容が抽出されます。
- このモードではプランの作成のみを行い、実装は行いません。`, reviewFeedback)
}

// BuildPlanExecutionPrompt constructs the prompt for plan execution mode.
func BuildPlanExecutionPrompt(originalPrompt, planContent string, validationError ...string) string {
	var result strings.Builder
	result.WriteString(`以下のプランに従って実装を行ってください。

元のタスク:
`)
	result.WriteString(originalPrompt)
	result.WriteString(`

実行すべきプラン:
`)
	result.WriteString(planContent)

	// Validation errors
	if len(validationError) > 0 && validationError[0] != "" {
		result.WriteString(`

Validation failures:
`)
		result.WriteString(validationError[0])
		result.WriteString(`

上記のvalidationエラーを修正し、プランに従って実装を完了してください。`)
	} else {
		result.WriteString(`

プランに従って実装を完了してください。`)
	}

	return result.String()
}
