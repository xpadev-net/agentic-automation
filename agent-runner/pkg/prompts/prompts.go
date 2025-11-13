package prompts

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-runner/pkg/context"
)

// BuildTaskPrompt builds the full prompt for the agent.
// It combines the original prompt with previous attempts, CI logs, and validation errors.
func BuildTaskPrompt(prompt, previousAttempts, ciLogs string, validationError ...string) string {
	var result strings.Builder

	// Original prompt
	result.WriteString("<task>\n")
	result.WriteString(prompt)
	result.WriteString("\n</task>")

	// Previous attempts
	if previousAttempts != "" {
		var attempts []context.PreviousAttempt
		if err := json.Unmarshal([]byte(previousAttempts), &attempts); err == nil && len(attempts) > 0 {
			result.WriteString("\n\n<previous_attempts>\n")
			for _, attempt := range attempts {
				result.WriteString(fmt.Sprintf("- Retry #%d: %s\n", attempt.RetryCount, attempt.Error))
				if attempt.CILogs != "" {
					result.WriteString(fmt.Sprintf("  CI Logs: %s\n", attempt.CILogs))
				}
			}
			result.WriteString("</previous_attempts>")
		}
	}

	// CI logs (if not already included in previous attempts)
	if ciLogs != "" {
		result.WriteString("\n\n<ci_logs>\n")
		result.WriteString(ciLogs)
		result.WriteString("\n</ci_logs>")
	}

	// Validation errors
	if len(validationError) > 0 && validationError[0] != "" {
		result.WriteString("\n\n<validation_errors>\n")
		result.WriteString(validationError[0])
		result.WriteString("\n</validation_errors>")
		result.WriteString("\n\n上記の情報を基に、タスクを実行してください。バリデーションエラーがある場合は、それらを修正してから続行してください。")
	} else {
		result.WriteString("\n\n上記の情報を基に、タスクを実行してください。")
	}

	return result.String()
}

// BuildPlanCreationPrompt constructs the prompt for plan creation mode.
func BuildPlanCreationPrompt(reviewFeedback string) string {
	return fmt.Sprintf(`以下のレビューフィードバックを分析し、対応プランを作成してください。

<review_feedback>
%s
</review_feedback>

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

<original_task>
`)
	result.WriteString(originalPrompt)
	result.WriteString(`
</original_task>

<plan>
`)
	result.WriteString(planContent)
	result.WriteString(`
</plan>`)

	// Validation errors
	if len(validationError) > 0 && validationError[0] != "" {
		result.WriteString(`

<validation_errors>
`)
		result.WriteString(validationError[0])
		result.WriteString(`
</validation_errors>

上記のvalidationエラーを修正し、プランに従って実装を完了してください。`)
	} else {
		result.WriteString(`

プランに従って実装を完了してください。`)
	}

	return result.String()
}

// BuildPRTitleGenerationPrompt builds the prompt for PR title and body generation.
func BuildPRTitleGenerationPrompt(issueNumber int, issuePrompt, changedFilesList, commitMsg, diffCommand string) string {
	return fmt.Sprintf("以下の情報を基に、Pull Requestのタイトルと概要を生成してください。\n\n<issue>\n<number>%d</number>\n<description>%s</description>\n</issue>\n\n<changed_files>\n%s\n</changed_files>\n\n<commit_message>\n%s\n</commit_message>\n\n作業ディレクトリで `%s` を実行して変更内容を確認し、それを基にPRタイトルと概要を生成してください。\n\n出力形式:\n以下のXML形式で出力してください。\n<title>PRタイトル</title>\n<body>PR概要（Markdown形式可）</body>", issueNumber, issuePrompt, changedFilesList, commitMsg, diffCommand)
}

// BuildCommitMessageGenerationPrompt builds the prompt for commit message generation.
func BuildCommitMessageGenerationPrompt(issueNumber int, issueInfo, changedFilesList, stagedDiff string) string {
	return fmt.Sprintf("以下の情報を基に、コミットメッセージを生成してください。\n\n<issue>\n<number>%d</number>\n<description>%s</description>\n</issue>\n\n<changed_files>\n%s\n</changed_files>\n\n<staged_diff>\n%s\n</staged_diff>\n\n上記の変更内容を基に、適切なコミットメッセージを生成してください。\n\n重要: このタスクはコミットメッセージの生成のみを行います。ファイルを変更したり、コードを編集したりしないでください。ステージされた変更内容を確認し、適切なコミットメッセージを生成するだけです。\n\n出力形式:\n以下のXML形式で出力してください。\n<commit_message>コミットメッセージ（必ずIssue番号 #%d を含めてください）</commit_message>", issueNumber, issueInfo, changedFilesList, stagedDiff, issueNumber)
}

// BuildConflictResolutionPrompt builds the prompt for merge conflict resolution.
func BuildConflictResolutionPrompt(issueID int, conflictFilesList, headSHA, mergeHeadSHA, conflictDiffs string) string {
	return fmt.Sprintf(`マージコンフリクトを解消してください。

<issue>
<number>%d</number>
</issue>

<conflict_files>
%s
</conflict_files>

<git_info>
<head>%s</head>
<merge_head>%s</merge_head>
</git_info>

<conflict_diffs>
%s
</conflict_diffs>

各ファイルについて、HEAD（現在のブランチ）とMERGE_HEAD（マージ元ブランチ）の変更を統合し、適切に解消してください。
- ビルドを壊さない修正を心がけてください
- テストが通過するようにしてください
- 両方の変更を可能な限り保持してください
- コンフリクトマーカー（<<<<<<<, =======, >>>>>>>）を削除し、解消済みのコードに置き換えてください

すべてのコンフリクトファイルを解消し、コンフリクトマーカーを完全に削除してください。`, issueID, conflictFilesList, headSHA, mergeHeadSHA, conflictDiffs)
}
