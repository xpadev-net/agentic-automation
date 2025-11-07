# レビュー指摘対応のプラン作成・実行機能実装プラン

## 概要

**すべてのレビューコメント**（Codex bot・ユーザー問わず）に対して、以下の2段階の処理を実装します：
1. **プラン作成Pod**: 指摘事項を検討し、プランを作成または却下
2. **プラン実行Pod**: プランが作成された場合、そのプランを実行

**注意**:
- Codex botの**approve検出**は`issue_comment`イベントで処理（Phase 0で移行）
- `pr_review_comment`イベントでは**レビュー指摘のみ**を処理し、approve検出は行わない
- レビュー指摘はCodex/ユーザーを区別せず一律でプラン作成フローに進む

## 背景

### 現状の問題点
- レビューフィードバックで指摘事項があった場合、現在は手動で対応する必要がある
- AIエージェントが指摘事項を自動的に検討し、対応プランを作成・実行する機能がない
- 指摘事項の対応が属人化し、対応が遅れる可能性がある

### 実装方針
- 既存の`agent-runner`イメージを再利用（モード切り替えで対応）
- プラン作成Podはwrite権限を剥奪し、プロンプトで制御
- プランはAPI経由でOperatorに返し、それを実行Podに渡す
- プラン却下時はプラン実行Podを起動せず、PRにコメントを投稿
- 既存の`claude-code`/`cursor-agent`を使用
- プラン作成Pod完了後に自動的にプラン実行Podを起動

## 実装計画

### Phase 0: Codex approve検出機能の移行

**目的**: Codexのapprove検出を`pr_review_comment`から`issue_comment`に移行

**背景**:
- Codexのapproveは`pr_review_comment`ではなく通常の`issue_comment`（PRに関連する通常のコメント）で届く
- Codexの`pr_review_comment`は原則レビュー内容が含まれる場合のみなので、approve検出の特化対応は不要
- レビュー指摘の監視は`pr_review_comment`のままで良い
- Codex bot判定は`issue_comment`でのみ行い、`pr_review_comment`では行わない

**修正対象ファイル**:
- `internal/webhooks/handlers/pr_review_comment.go`
- `internal/webhooks/handlers/issue_comment.go`
- `internal/services/codex_approval.go` (IsCodexBot()メソッドの追加)

**実装内容**:

1. **`pr_review_comment`ハンドラーからapprove検出処理を削除**
   ```go
   // internal/webhooks/handlers/pr_review_comment.go
   // HandlePullRequestReviewCommentWithDeps内で、以下の処理を削除:
   
   // 削除対象: Step 4.5のCodex承認コメント検知処理
   // detector := services.NewCodexApprovalDetector(logger)
   // if detector.DetectApproval(...) {
   //     // マージ条件再評価処理
   //     // ...
   // }
   ```

2. **`issue_comment`ハンドラーにapprove検出処理を追加**
   ```go
   // internal/webhooks/handlers/issue_comment.go
   // HandleIssueCommentWithDeps内で、/run-agentトリガー検出の前に追加

   // Step 4.5: Codex approve検出（PRに関連するIssueコメントの場合のみ）
   // 注意: Codex bot判定は`issue_comment`（PRに関連する通常のコメント）でのみ行う
   // IssueがPRに関連しているかチェック
   prRepo := repositories.NewPullRequestRepository(db)
   pr, err := prRepo.FindByRepoAndNumber(payload.Repository.FullName, payload.Issue.Number)
   if err == nil && pr != nil {
       // PRに関連するIssueコメントの場合、approveを検出
       // DetectApprovalはCodex bot判定を内部で行うため、外側での事前チェックは不要
       detector := services.NewCodexApprovalDetector(logger)
       if detector.DetectApproval(payload.Comment.Body, payload.Comment.User.Login, payload.Comment.User.ID) {
           logger.Info("Codex approval detected in issue comment; re-evaluating merge conditions",
               zap.String("delivery_id", deliveryID),
               zap.Int("issue_number", payload.Issue.Number),
               zap.String("repo", payload.Repository.FullName),
           )
           
           // ReviewFeedbackレコード作成（承認検出時）
           reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
           commentID := int64(payload.Comment.ID)
           content := payload.Comment.Body
           
           // 既存の'requested'があれば'received'へ更新、無ければ'received'を新規作成
           if list, err := reviewFeedbackRepo.FindByPRIDAndStatus(pr.ID, "requested"); err == nil && len(list) > 0 {
               latest := list[0]
               reviewFeedbackRepo.UpdateToReceived(latest.ID, content, true, &commentID)
           } else {
               reviewFeedbackRepo.CreateReceivedReview(pr.ID, content, true, &commentID)
           }
           
           // マージ条件再評価処理（既存のpr_review_commentハンドラーの処理を再利用）
           // CIStatusProvider/CodexApprovalCheckerのアダプタを作成
           ciRepo := repositories.NewCIStatusRepository()
           ciProvider := &ciStatusProviderAdapter{repo: ciRepo, prID: pr.ID}
           conflictDetector := services.NewMergeConflictDetector(githubClient, logger)
           checker := services.NewMergeConditionChecker(ciProvider, &alwaysApprovedChecker{}, conflictDetector, logger)
           
           res, err := checker.Check(ctx, owner, repo, pr.Number)
           if err != nil {
               logger.Error("merge condition check failed", zap.Error(err))
               c.Error(err)
               return
           }
           
           if res.Mergeable {
               // 自動マージ処理
               am := services.NewAutoMergeService(appGitHubClient, logger)
               mergeRes, mergeErr := am.AttemptAutoMerge(ctx, owner, repo, pr.Number)
               // ... マージ結果処理 ...
           }
           
           // approve検出時は処理を終了（/run-agentトリガー検出はスキップ）
           c.JSON(http.StatusOK, gin.H{
               "status":      "codex_approval_detected",
               "delivery_id": deliveryID,
           })
           return
       }
   }
   
   // 以降は既存の/run-agentトリガー検出処理を続行
   ```

3. **CodexApprovalDetectorにIsCodexBot()メソッドを追加**
   ```go
   // internal/services/codex_approval.go
   // CodexApprovalDetectorにpublicメソッドを追加

   // IsCodexBot checks if the given username and user ID match the Codex bot.
   // This is a public wrapper around the private isCodexBot method.
   // 注意: 通常はDetectApproval()を使用すれば内部でbot判定が行われるため、
   // このメソッドは情報取得目的など、特別な理由がある場合にのみ使用すること
   func (s *CodexApprovalDetector) IsCodexBot(username string, userID int64) bool {
       return s.isCodexBot(username, userID)
   }
   ```

4. **ヘルパー関数の追加（必要に応じて）**
   ```go
   // internal/webhooks/handlers/issue_comment.go
   // pr_review_commentハンドラーから移植
   
   // ciStatusProviderAdapter は PR に紐づく最新の aggregated CI 状態を返す軽量アダプタ
   type ciStatusProviderAdapter struct {
       repo *repositories.CIStatusRepository
       prID int
   }
   
   func (a *ciStatusProviderAdapter) GetAggregatedState(_ context.Context, _ string, _ string, _ int) (services.CIState, error) {
       // pr_review_comment.goの実装をそのまま移植
   }
   
   // alwaysApprovedChecker は本イベントで承認検知済みのため常に true を返すアダプタ
   type alwaysApprovedChecker struct{}
   
   func (a *alwaysApprovedChecker) IsApproved(_ context.Context, _ string, _ string, _ int) (bool, error) {
       return true, nil
   }
   ```

**テスト項目**:
- [ ] `CodexApprovalDetector.IsCodexBot()`メソッドが正しく動作すること
- [ ] `pr_review_comment`ハンドラーからapprove検出処理が削除されていること
- [ ] `issue_comment`ハンドラーでPRに関連するIssueコメントの場合にapprove検出が動作すること
- [ ] `issue_comment`ハンドラーでPRに関連しないIssueコメントの場合はapprove検出がスキップされること
- [ ] approve検出時にReviewFeedbackレコードが正しく作成/更新されること
- [ ] approve検出時にマージ条件再評価が正常に動作すること
- [ ] approve検出時に/run-agentトリガー検出がスキップされること

---

### Phase 1: データモデル拡張

**目的**: プラン作成・実行の状態を追跡するためのデータモデルを拡張

**修正対象ファイル**:
- `internal/models/review_feedback.go`
- `internal/models/agent_run.go`
- `migrations/000006_add_review_feedback_plan_fields.sql` (新規)
- `migrations/000007_add_agent_run_execution_mode.sql` (新規)

**実装内容**:

1. **ReviewFeedbackモデルの拡張**
   ```go
   // internal/models/review_feedback.go
   type ReviewFeedback struct {
       // ... 既存フィールド ...

       // プラン作成・実行関連フィールド
       // 状態遷移: pending → creating → created → executed
       //                              ↘ rejected
       // creating: プラン作成Pod起動済み（多重起動防止用）
       PlanCreationStatus string    `gorm:"type:enum('pending','creating','created','rejected','executed');default:'pending'"`
       PlanContent        *string   `gorm:"type:text"` // プラン内容
       PlanAgentRunID     *int      `gorm:"column:plan_agent_run_id;index"` // プラン作成用AgentRun ID
       ExecutionAgentRunID *int     `gorm:"column:execution_agent_run_id;index"` // プラン実行用AgentRun ID
   }
   ```

   **状態遷移図**:
   ```
   pending (初期状態: レビューフィードバック受信直後)
      ↓
      creating (プラン作成Pod起動済み: 多重起動防止)
      ↓
      ├─→ created (プラン作成成功) → executed (プラン実行完了)
      │                                   ↑
      │                                   │ (失敗時は created に戻る)
      │
      └─→ rejected (プラン却下)
   ```

2. **AgentRunモデルの拡張**
   ```go
   // internal/models/agent_run.go
   type AgentRun struct {
       // ... 既存フィールド ...
       
       // 実行モード関連フィールド
       ExecutionMode     string    `gorm:"type:enum('normal','plan_creation','plan_execution');default:'normal'"`
       PlanContent       *string   `gorm:"type:text"` // プラン実行モード時に使用
       ReviewFeedbackID  *int      `gorm:"column:review_feedback_id;index"` // プラン作成/実行に関連するReviewFeedback ID
   }
   ```

3. **マイグレーションファイル作成**
   ```sql
   -- migrations/000006_add_review_feedback_plan_fields.sql
   -- プラン作成・実行関連フィールドを追加
   -- 状態: pending → creating → created → executed (または rejected)
   -- creating: プラン作成Pod起動済み（多重起動防止用）
   ALTER TABLE review_feedback
       ADD COLUMN plan_creation_status ENUM('pending','creating','created','rejected','executed') DEFAULT 'pending',
       ADD COLUMN plan_content TEXT,
       ADD COLUMN plan_agent_run_id INT,
       ADD COLUMN execution_agent_run_id INT,
       ADD INDEX idx_plan_agent_run_id (plan_agent_run_id),
       ADD INDEX idx_execution_agent_run_id (execution_agent_run_id);
   ```

   ```sql
   -- migrations/000007_add_agent_run_execution_mode.sql
   ALTER TABLE agent_runs
       ADD COLUMN execution_mode ENUM('normal','plan_creation','plan_execution') DEFAULT 'normal',
       ADD COLUMN plan_content TEXT,
       ADD COLUMN review_feedback_id INT,
       ADD INDEX idx_review_feedback_id (review_feedback_id);
   ```

**テスト項目**:
- [ ] マイグレーションが正常に実行されること
- [ ] ReviewFeedbackモデルでプラン関連フィールドが正しく保存・取得できること
- [ ] AgentRunモデルで実行モード関連フィールドが正しく保存・取得できること

---

### Phase 2: agent-runner拡張（実行モード対応）

**目的**: agent-runnerにプラン作成・実行モードを追加

**修正対象ファイル**:
- `agent-runner/main.go`
- `agent-runner/pkg/context/issue.go`
- `agent-runner/pkg/reporter/client.go`

**実装内容**:

1. **実行モードフラグの追加**
   ```go
   // agent-runner/main.go
   var (
       issueID          int
       repo             string
       prompt           string
       previousAttempts string
       ciLogs           string
       executionMode    string // 新規追加
   )

   rootCmd.Flags().StringVar(&executionMode, "execution-mode", "normal", "Execution mode: normal, plan_creation, plan_execution")
   ```

2. **プラン作成モードの処理**
   ```go
   // agent-runner/main.go
   // Run関数のシグネチャを拡張してレビュー内容を受け取る
   func Run(issueID int, repo, prompt, previousAttempts, ciLogs, executionMode, reviewFeedbackContent string) error {
       // ... 既存の処理 ...

       var fullPrompt string

       // プラン作成モードの場合
       if executionMode == "plan_creation" {
           // write権限を剥奪（CURSOR_ALLOW_WRITE=false）
           envCfg.CursorAllowWrite = false

           // レビューフィードバック内容からプラン作成用プロンプトを構築
           reviewContent := reviewFeedbackContent
           if reviewContent == "" {
               reviewContent = os.Getenv("REVIEW_FEEDBACK_CONTENT")
           }
           if reviewContent == "" {
               return fmt.Errorf("REVIEW_FEEDBACK_CONTENT is required for plan_creation mode")
           }
           fullPrompt = context.BuildPlanCreationPrompt(reviewContent)

           // エージェント実行
           agentOutput, err := executor.Execute(envCfg.WorkDir, fullPrompt)
           if err != nil {
               return fmt.Errorf("agent execution failed: %w", err)
           }

           // ファイル変更チェックをスキップ
           // コミット/PR作成をスキップ

           // プラン作成結果を報告
           // プランが作成されたか、却下されたかを判定
           planContent, rejected, rejectionReason := parser.ParsePlanResult(agentOutput)
           if rejected {
               return reporterClient.ReportPlanRejection(rejectionReason, agentOutput)
           }
           return reporterClient.ReportPlanCreation(planContent, agentOutput)
       }

       // プラン実行モードの場合
       if executionMode == "plan_execution" {
           // プラン内容を環境変数から取得
           planContent := os.Getenv("PLAN_CONTENT")
           if planContent == "" {
               return fmt.Errorf("PLAN_CONTENT environment variable is required for plan_execution mode")
           }
           // プラン内容をプロンプトに含める
           fullPrompt = context.BuildPlanExecutionPrompt(prompt, planContent)
           // 通常の実行フローを続行
       } else {
           // 通常モード: 既存のプロンプト構築ロジック
           fullPrompt = prompt // または既存のプロンプト構築
       }

       // ... 既存の処理（通常モード/プラン実行モード共通） ...
   }
   ```

3. **parsePlanResult関数の実装**
   ```go
   // agent-runner/pkg/parser/plan.go (新規ファイル)
   package parser

   import (
       "fmt"
       "strings"
   )

   const (
       PlanCreatedMarker  = "PLAN_CREATED\n\n"
       PlanRejectedMarker = "PLAN_REJECTED\n\n"
   )

   // parsePlanResult parses the agent output to extract plan content or rejection reason.
   // Returns: (planContent, rejected, rejectionReason)
   func ParsePlanResult(output string) (string, bool, string) {
       trimmed := strings.TrimSpace(output)

       // Check for PLAN_CREATED marker
       if strings.HasPrefix(trimmed, PlanCreatedMarker) {
           content := strings.TrimPrefix(trimmed, PlanCreatedMarker)
           content = strings.TrimSpace(content)
           if content == "" {
               // Empty plan content - treat as rejection
               return "", true, "プラン内容が空です"
           }
           return content, false, ""
       }

       // Check for PLAN_REJECTED marker
       if strings.HasPrefix(trimmed, PlanRejectedMarker) {
           reason := strings.TrimPrefix(trimmed, PlanRejectedMarker)
           reason = strings.TrimSpace(reason)
           if reason == "" {
               reason = "プランが却下されました（理由不明）"
           }
           return "", true, reason
       }

       // Neither marker found - parse error, treat as rejection
       return "", true, fmt.Sprintf("プラン出力のパースに失敗しました。期待される形式: '%s[内容]' または '%s[理由]'",
           PlanCreatedMarker, PlanRejectedMarker)
   }
   ```

4. **プラン作成用プロンプト構築**
   ```go
   // agent-runner/pkg/context/issue.go
   func BuildPlanCreationPrompt(reviewFeedback string) string {
       return fmt.Sprintf(`以下のレビューフィードバックを分析し、対応プランを作成してください。

レビューフィードバック:
%s

プラン作成の要件:
1. 指摘事項を整理し、対応が必要な項目を特定してください
2. 各項目に対する具体的な対応方法を提案してください
3. 対応が困難または不要な場合は、却下理由を明確にしてください

プランの形式:
- プランを作成する場合: "PLAN_CREATED\n\n[プラン内容]"
- プランを却下する場合: "PLAN_REJECTED\n\n[却下理由]"

注意: このモードではファイルの変更は行いません。プランの作成のみを行ってください。`, reviewFeedback)
   }
   ```

5. **プラン実行用プロンプト構築**
   ```go
   // agent-runner/pkg/context/issue.go
   func BuildPlanExecutionPrompt(originalPrompt, planContent string) string {
       return fmt.Sprintf(`以下のプランに従って実装を実行してください。

元のタスク:
%s

実行すべきプラン:
%s

プランに従って実装を完了してください。`, originalPrompt, planContent)
   }
   ```

6. **プラン作成結果報告メソッドの追加**
   ```go
   // agent-runner/pkg/reporter/client.go
   type PlanReportRequest struct {
       Status          string `json:"status"` // "plan_created" | "plan_rejected"
       AgentType       string `json:"agent_type"`
       PlanContent     string `json:"plan_content,omitempty"`
       RejectionReason string `json:"rejection_reason,omitempty"`
       Logs            string `json:"logs,omitempty"`
   }

   func (c *Client) ReportPlanCreation(planContent, logs string) error {
       req := &PlanReportRequest{
           Status:      "plan_created",
           AgentType:   c.agentType,
           PlanContent: planContent,
           Logs:        logs,
       }
       return sendPlanReport(c, req)
   }

   func (c *Client) ReportPlanRejection(rejectionReason, logs string) error {
       req := &PlanReportRequest{
           Status:          "plan_rejected",
           AgentType:       c.agentType,
           RejectionReason: rejectionReason,
           Logs:            logs,
       }
       return sendPlanReport(c, req)
   }
   ```

**テスト項目**:
- [ ] 実行モードフラグが正しく解析されること
- [ ] プラン作成モードでwrite権限が剥奪されること
- [ ] プラン作成モードでファイル変更チェックがスキップされること
- [ ] プラン作成用プロンプトが正しく構築されること
- [ ] プラン実行用プロンプトが正しく構築されること
- [ ] プラン作成結果が正しく報告されること

---

### Phase 3: Operator API拡張（プラン作成結果処理）

**目的**: プラン作成結果を受信し、プラン実行Podを起動する処理を追加

**修正対象ファイル**:
- `internal/webhooks/handlers/agent_report.go`
- `internal/services/kubernetes_job.go`

**実装内容**:

1. **プラン作成結果のリクエスト構造体追加**
   ```go
   // internal/webhooks/handlers/agent_report.go
   type PlanReportRequest struct {
       Status          string `json:"status" binding:"required,oneof=plan_created plan_rejected"`
       AgentType       string `json:"agent_type" binding:"required,oneof=claude-code cursor-agent"`
       PlanContent     string `json:"plan_content,omitempty"`
       RejectionReason string `json:"rejection_reason,omitempty"`
       Logs            string `json:"logs,omitempty"`
   }
   ```

2. **プラン作成結果処理の追加**
   ```go
   // internal/webhooks/handlers/agent_report.go
   func HandleAgentReport(c *gin.Context) {
       // ... 既存の処理 ...
       
       // プラン作成結果の場合
       if req.Status == "plan_created" || req.Status == "plan_rejected" {
           return handlePlanReport(c, agentRunID, req)
       }
       
       // ... 既存の処理 ...
   }

   func handlePlanReport(c *gin.Context, agentRunID int, req *PlanReportRequest) {
       // AgentRunを取得
       agentRun, err := agentRunRepo.GetByID(agentRunID)
       if err != nil {
           // エラー処理
       }
       
       // ReviewFeedbackを取得
       reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
       reviewFeedback, err := reviewFeedbackRepo.FindByID(*agentRun.ReviewFeedbackID)
       if err != nil {
           // エラー処理
       }
       
       if req.Status == "plan_created" {
           // プラン内容を保存
           reviewFeedback.PlanContent = &req.PlanContent
           reviewFeedback.PlanCreationStatus = "created"
           reviewFeedback.PlanAgentRunID = &agentRunID
           reviewFeedbackRepo.Update(reviewFeedback)
           
           // プラン実行Podを起動
           issue, _ := repositories.NewIssueRepository().FindByID(agentRun.IssueID)
           pr, _ := repositories.NewPullRequestRepository().FindByID(*reviewFeedback.PRID)

           executionAgentRun := &models.AgentRun{
               IssueID:          agentRun.IssueID,
               PRID:             reviewFeedback.PRID,
               AgentType:        agentRun.AgentType,
               ExecutionMode:    "plan_execution",
               PlanContent:      &req.PlanContent,
               ReviewFeedbackID: &reviewFeedback.ID,
               State:            "queued",
           }
           agentRunRepo.Create(executionAgentRun)

           jobService := services.NewKubernetesJobService(kubernetesClient, logger)
           // ブランチ名: 既存PRがあればそのブランチを使用、なければ新規作成
           var branchName string
           if pr != nil && pr.BranchName != "" {
               // 既存PRのブランチを再利用（PRを更新する）
               branchName = pr.BranchName
           } else {
               // 新規ブランチ作成
               branchName = fmt.Sprintf("feature/issue-%d", issue.Number)
           }
           job, err := jobService.CreateJobForPlanExecution(ctx, executionAgentRun, issue, req.PlanContent, branchName)
           if err != nil {
               // エラー処理
           }
           
           reviewFeedback.ExecutionAgentRunID = &executionAgentRun.ID
           // 注意: 実行中の状態はAgentRunのStateで管理するため、ここでは変更しない
           reviewFeedbackRepo.Update(reviewFeedback)
           
           c.JSON(http.StatusOK, gin.H{
               "message": "Plan created and execution job started",
               "agent_run_id": executionAgentRun.ID,
           })
       } else if req.Status == "plan_rejected" {
           // プラン却下時はPRにコメントを投稿
           reviewFeedback.PlanCreationStatus = "rejected"
           reviewFeedbackRepo.Update(reviewFeedback)

           // PRにコメントを投稿
           pr, _ := repositories.NewPullRequestRepository().FindByID(reviewFeedback.PRID)
           repoParts := strings.SplitN(pr.Repo, "/", 2)
           if len(repoParts) != 2 {
               logger.Error("Invalid repository full name format",
                   zap.String("repo", pr.Repo),
                   zap.Int("agent_run_id", agentRunID),
               )
               c.JSON(http.StatusInternalServerError, gin.H{
                   "error": "Invalid repository format",
               })
               return
           }
           owner, repo := repoParts[0], repoParts[1]
           
           githubClient, _ := clients.NewGitHubAppClient(logger)
           rawClient, _ := githubClient.ForRepo(ctx, owner, repo)
           ghClient := clients.NewFromGitHub(rawClient, logger)
           
           comment := fmt.Sprintf("⚠️ プラン作成が却下されました。\n\n理由:\n%s", req.RejectionReason)
           ghClient.CreateIssueComment(ctx, owner, repo, pr.Number, comment)
           
           c.JSON(http.StatusOK, gin.H{
               "message": "Plan rejected",
           })
       }
   }
   ```

3. **プラン実行Pod起動メソッドの追加**
   ```go
   // internal/services/kubernetes_job.go
   func (s *kubernetesJobService) CreateJobForPlanExecution(
       ctx context.Context,
       agentRun *models.AgentRun,
       issue *models.Issue,
       planContent string,
       branchName string,
   ) (*batchv1.Job, error) {
       // プラン実行用プロンプトを構築
       prompt := context.BuildPlanExecutionPrompt(issue.Title, planContent)
       
       // JobConfigを構築
       jobConfig := &clients.JobConfig{
           AgentRunID:       agentRun.ID,
           RetryCount:       0,
           IssueID:          issue.Number,
           Repo:             issue.Repo,
           Prompt:           prompt,
           AgentType:        agentRun.AgentType,
           AgentRunnerImage: agentRunnerImage,
           TimeoutMinutes:   timeoutMinutes,
           BranchName:       branchName,
           ExecutionMode:    "plan_execution", // 新規追加
           PlanContent:      planContent,      // 新規追加
       }
       
       // Kubernetes Jobを作成
       jobName := s.kubernetesClient.GenerateJobName(agentRun.ID)
       return s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
   }
   ```

**テスト項目**:
- [ ] プラン作成結果が正しく受信されること
- [ ] プラン作成時にプラン内容が保存されること
- [ ] プラン作成時にプラン実行Podが起動されること
- [ ] プラン却下時にPRにコメントが投稿されること
- [ ] プラン却下時にプラン実行Podが起動されないこと

---

### Phase 4: レビューフィードバック処理拡張

**目的**: レビューフィードバック受信時にプラン作成Podを起動

**注意事項**:
- `pr_review_comment`イベントではapprove検出を行わない（Phase 0で`issue_comment`に移行済み）
- レビュー指摘の監視は`pr_review_comment`イベントのままで良い
- **すべてのレビューコメント**（Codexかユーザーかを区別せず）に対してプラン作成Podを起動する
- `pr_review_comment`ではCodex bot判定を行わず、一律で処理する
- Codex bot判定は`issue_comment`（PRに関連する通常のコメント）でのみ行う

**修正対象ファイル**:
- `internal/webhooks/handlers/pr_review_comment.go`
- `internal/services/kubernetes_job.go`

**実装内容**:

1. **プラン作成Pod起動ロジックの追加**
   ```go
   // internal/webhooks/handlers/pr_review_comment.go
   // HandlePullRequestReviewCommentWithDeps内で、@codex reviewトリガー検出後
   // または、レビューコメント受信時（approve検出は行わない）
   
   // すべてのレビューコメントを指摘事項として処理
   // Codex botのapprove検出は行わない（Phase 0でissue_commentに移行済み）
   // Codex botかユーザーかを区別せず、一律で処理する

   // レビューコメントのフィルタリング
   commentBody := strings.TrimSpace(payload.Comment.Body)
   if commentBody == "" {
       logger.Info("Skipping empty review comment",
           zap.Int("pr_number", pr.Number),
           zap.String("delivery_id", deliveryID),
       )
       return
   }
   // 軽微なコメント（短文）をスキップ
   minCommentLength := 20 // 設定可能な閾値（環境変数等で上書き可能）
   if len(commentBody) < minCommentLength {
       logger.Info("Skipping too-short review comment",
           zap.Int("comment_length", len(commentBody)),
           zap.Int("pr_number", pr.Number),
       )
       return
   }

   // プラン作成Pod起動処理
   {
       // ReviewFeedbackを作成または更新（approval_detected=false）
       reviewFeedbackRepo := deps.ReviewFeedbackRepository
       if reviewFeedbackRepo == nil {
           reviewFeedbackRepo = repositories.NewReviewFeedbackRepository()
       }
       
       commentID := int64(payload.Comment.ID)
       var reviewFeedback *models.ReviewFeedback

       // 既存の'requested'があれば'received'へ更新
       if list, err := reviewFeedbackRepo.FindByPRIDAndStatus(pr.ID, "requested"); err == nil && len(list) > 0 {
           latest := list[0]
           reviewFeedbackRepo.UpdateToReceived(latest.ID, payload.Comment.Body, false, &commentID)
           reviewFeedback = latest
       } else {
           // 新規作成（approval_detected=false）
           reviewFeedback = &models.ReviewFeedback{
               PRID:             pr.ID,
               Content:          &payload.Comment.Body,
               Status:           "received",
               ApprovalDetected: false,
               GitHubCommentID:  &commentID,
           }
           reviewFeedbackRepo.Create(reviewFeedback)
       }
       
       // プラン作成Podを起動（approval_detected=falseの場合のみ）
       if reviewFeedback != nil && !reviewFeedback.ApprovalDetected && reviewFeedback.PlanCreationStatus == "pending" {
           // Issueを取得
           issueRepo := repositories.NewIssueRepository()
           issue, err := issueRepo.FindByID(*pr.IssueID)
           if err != nil {
               logger.Error("Failed to get Issue for plan creation", zap.Error(err))
               return
           }

           // AgentTypeを検出（Issueラベルベース）
           agentTypeDetectorService := services.NewAgentTypeDetector(logger)
           agentType := agentTypeDetectorService.DetectAgentType(issue)

           // AgentRunを作成
           agentRunRepo := repositories.NewAgentRunRepository(config.GetDB())
           agentRun := &models.AgentRun{
               IssueID:          issue.Number,
               PRID:             &pr.ID,
               AgentType:        agentType, // Issueラベルから検出
               ExecutionMode:    "plan_creation",
               ReviewFeedbackID: &reviewFeedback.ID,
               State:            "queued",
           }
           agentRunRepo.Create(agentRun)
           
           // プラン作成Podを起動
           kubernetesClient := clients.NewKubernetesClient(logger)
           jobService := services.NewKubernetesJobService(kubernetesClient, logger)
           // ブランチ名: 既存PRがあればそのブランチを使用、なければ新規作成
           var branchName string
           if pr.BranchName != "" {
               // 既存PRのブランチを再利用（PRを更新する）
               branchName = pr.BranchName
           } else {
               // 新規ブランチ作成
               branchName = fmt.Sprintf("feature/issue-%d", issue.Number)
           }
           
           job, err := jobService.CreateJobForPlanCreation(ctx, agentRun, issue, reviewFeedback, branchName)
           if err != nil {
               logger.Error("Failed to create plan creation job", zap.Error(err))
               return
           }
           
           // ReviewFeedbackを更新（多重起動防止のためcreatingに遷移）
           reviewFeedback.PlanCreationStatus = "creating"
           reviewFeedback.PlanAgentRunID = &agentRun.ID
           reviewFeedbackRepo.Update(reviewFeedback)
           
           logger.Info("Plan creation job started",
               zap.Int("agent_run_id", agentRun.ID),
               zap.Int("review_feedback_id", reviewFeedback.ID),
           )
       }
   } // プラン作成Pod起動処理終了
   ```

2. **プラン作成Pod起動メソッドの追加**
   ```go
   // internal/services/kubernetes_job.go
   func (s *kubernetesJobService) CreateJobForPlanCreation(
       ctx context.Context,
       agentRun *models.AgentRun,
       issue *models.Issue,
       reviewFeedback *models.ReviewFeedback,
       branchName string,
   ) (*batchv1.Job, error) {
       // レビュー内容を取得
       reviewContent := ""
       if reviewFeedback.Content != nil {
           reviewContent = *reviewFeedback.Content
       }
       if reviewContent == "" {
           return nil, fmt.Errorf("review feedback content is empty")
       }

       // JobConfigを構築
       // 注意: promptはagent-runner側でBuildPlanCreationPrompt()を呼ぶため、
       // ここでは簡易的な説明のみを渡す
       jobConfig := &clients.JobConfig{
           AgentRunID:            agentRun.ID,
           RetryCount:            0,
           IssueID:               issue.Number,
           Repo:                  issue.Repo,
           Prompt:                fmt.Sprintf("Plan creation for issue #%d", issue.Number),
           AgentType:             agentRun.AgentType,
           AgentRunnerImage:      agentRunnerImage,
           TimeoutMinutes:        timeoutMinutes,
           BranchName:            branchName,
           ExecutionMode:         "plan_creation",     // 新規追加
           ReviewFeedbackContent: reviewContent,       // 新規追加: レビュー内容
           CursorAllowWrite:      false,               // write権限を剥奪
       }

       // Kubernetes Jobを作成
       jobName := s.kubernetesClient.GenerateJobName(agentRun.ID)
       return s.kubernetesClient.CreateJob(ctx, jobName, jobConfig)
   }
   ```

3. **JobConfig構造体の拡張**
   ```go
   // internal/clients/kubernetes.go
   type JobConfig struct {
       // ... 既存フィールド ...
       ExecutionMode          string // 新規追加: "normal", "plan_creation", "plan_execution"
       PlanContent            string // 新規追加: プラン実行モード時に使用
       ReviewFeedbackContent  string // 新規追加: プラン作成モード時に使用
       CursorAllowWrite       bool   // 新規追加: cursor-agentのwrite権限制御
   }
   ```

4. **CreateJob()メソッドでの環境変数/args設定**
   ```go
   // internal/clients/kubernetes.go
   func (c *KubernetesClient) CreateJob(ctx context.Context, jobName string, config *JobConfig) (*batchv1.Job, error) {
       // ... 既存の処理 ...

       // 環境変数の構築
       env := []corev1.EnvVar{
           {Name: "AGENT_RUN_ID", Value: strconv.Itoa(config.AgentRunID)},
           {Name: "OPERATOR_API_URL", Value: c.operatorAPIURL},
           {Name: "OPERATOR_API_TOKEN", Value: c.operatorAPIToken},
           {Name: "ANTHROPIC_API_KEY", Value: c.anthropicAPIKey},
           {Name: "CURSOR_API_KEY", Value: c.cursorAPIKey},
           // ... 既存の環境変数 ...
       }

       // 実行モード別の環境変数追加
       if config.ExecutionMode == "plan_creation" {
           // プラン作成モード: レビュー内容を渡す
           if config.ReviewFeedbackContent != "" {
               // サイズチェック
               const maxEnvSize = 900 * 1024 // 900KB
               if len(config.ReviewFeedbackContent) > maxEnvSize {
                   // ConfigMapを使用
                   configMapName, err := c.createConfigMapForReviewContent(ctx, jobName, config.ReviewFeedbackContent)
                   if err != nil {
                       return nil, fmt.Errorf("failed to create ConfigMap: %w", err)
                   }
                   // ConfigMapをボリュームマウント
                   // (後述のvolumes/volumeMountsセクション参照)
                   env = append(env, corev1.EnvVar{
                       Name:  "REVIEW_FEEDBACK_CONTENT_FILE",
                       Value: "/config/review_feedback_content.txt",
                   })
               } else {
                   // 直接環境変数として渡す
                   env = append(env, corev1.EnvVar{
                       Name:  "REVIEW_FEEDBACK_CONTENT",
                       Value: config.ReviewFeedbackContent,
                   })
               }
           }
           if config.CursorAllowWrite {
               env = append(env, corev1.EnvVar{Name: "CURSOR_ALLOW_WRITE", Value: "true"})
           } else {
               env = append(env, corev1.EnvVar{Name: "CURSOR_ALLOW_WRITE", Value: "false"})
           }
       }

       if config.ExecutionMode == "plan_execution" {
           // プラン実行モード: プラン内容を渡す
           if config.PlanContent != "" {
               const maxEnvSize = 900 * 1024 // 900KB
               if len(config.PlanContent) > maxEnvSize {
                   // ConfigMapを使用
                   configMapName, err := c.createConfigMapForPlanContent(ctx, jobName, config.PlanContent)
                   if err != nil {
                       return nil, fmt.Errorf("failed to create ConfigMap: %w", err)
                   }
                   env = append(env, corev1.EnvVar{
                       Name:  "PLAN_CONTENT_FILE",
                       Value: "/config/plan_content.txt",
                   })
               } else {
                   env = append(env, corev1.EnvVar{
                       Name:  "PLAN_CONTENT",
                       Value: config.PlanContent,
                   })
               }
           }
       }

       // コマンドライン引数の構築
       args := []string{
           "--agent-run-id", strconv.Itoa(config.AgentRunID),
           "--issue-id", strconv.Itoa(config.IssueID),
           "--repo", config.Repo,
           "--prompt", config.Prompt,
           "--agent-type", config.AgentType,
           "--execution-mode", config.ExecutionMode, // ← 新規追加
       }

       if config.PreviousAttempts != "" {
           args = append(args, "--previous-attempts", config.PreviousAttempts)
       }
       if config.CILogs != "" {
           args = append(args, "--ci-logs", config.CILogs)
       }

       // Job仕様の構築
       job := &batchv1.Job{
           // ... 既存の仕様 ...
           Spec: batchv1.JobSpec{
               Template: corev1.PodTemplateSpec{
                   Spec: corev1.PodSpec{
                       Containers: []corev1.Container{
                           {
                               Name:  "agent-runner",
                               Image: config.AgentRunnerImage,
                               Args:  args,
                               Env:   env,
                               // ... その他の設定 ...
                           },
                       },
                       // ... RestartPolicy等 ...
                   },
               },
           },
       }

       return c.clientset.BatchV1().Jobs(c.namespace).Create(ctx, job, metav1.CreateOptions{})
   }
   ```

5. **ConfigMap作成ヘルパーメソッド**
   ```go
   // internal/clients/kubernetes.go
   func (c *KubernetesClient) createConfigMapForReviewContent(ctx context.Context, jobName, content string) (string, error) {
       configMapName := fmt.Sprintf("%s-review-content", jobName)
       configMap := &corev1.ConfigMap{
           ObjectMeta: metav1.ObjectMeta{
               Name:      configMapName,
               Namespace: c.namespace,
           },
           Data: map[string]string{
               "review_feedback_content.txt": content,
           },
       }
       _, err := c.clientset.CoreV1().ConfigMaps(c.namespace).Create(ctx, configMap, metav1.CreateOptions{})
       if err != nil {
           return "", err
       }
       return configMapName, nil
   }

   func (c *KubernetesClient) createConfigMapForPlanContent(ctx context.Context, jobName, content string) (string, error) {
       configMapName := fmt.Sprintf("%s-plan-content", jobName)
       configMap := &corev1.ConfigMap{
           ObjectMeta: metav1.ObjectMeta{
               Name:      configMapName,
               Namespace: c.namespace,
           },
           Data: map[string]string{
               "plan_content.txt": content,
           },
       }
       _, err := c.clientset.CoreV1().ConfigMaps(c.namespace).Create(ctx, configMap, metav1.CreateOptions{})
       if err != nil {
           return "", err
       }
       return configMapName, nil
   }
   ```

6. **agent-runnerでのファイル読み込み対応**
   ```go
   // agent-runner/main.go
   func Run(...) error {
       // ... 既存の処理 ...

       if executionMode == "plan_creation" {
           // 環境変数またはファイルからレビュー内容を取得
           reviewContent := os.Getenv("REVIEW_FEEDBACK_CONTENT")
           if reviewContent == "" {
               // ファイルパスが指定されている場合
               filePath := os.Getenv("REVIEW_FEEDBACK_CONTENT_FILE")
               if filePath != "" {
                   content, err := os.ReadFile(filePath)
                   if err != nil {
                       return fmt.Errorf("failed to read review content file: %w", err)
                   }
                   reviewContent = string(content)
               }
           }
           if reviewContent == "" {
               return fmt.Errorf("REVIEW_FEEDBACK_CONTENT is required")
           }
           fullPrompt = context.BuildPlanCreationPrompt(reviewContent)
       }

       if executionMode == "plan_execution" {
           // 環境変数またはファイルからプラン内容を取得
           planContent := os.Getenv("PLAN_CONTENT")
           if planContent == "" {
               filePath := os.Getenv("PLAN_CONTENT_FILE")
               if filePath != "" {
                   content, err := os.ReadFile(filePath)
                   if err != nil {
                       return fmt.Errorf("failed to read plan content file: %w", err)
                   }
                   planContent = string(content)
               }
           }
           if planContent == "" {
               return fmt.Errorf("PLAN_CONTENT is required")
           }
           fullPrompt = context.BuildPlanExecutionPrompt(prompt, planContent)
       }

       // ... 既存の処理 ...
   }
   ```

**テスト項目**:
- [ ] すべてのレビューコメント（Codex botかユーザーかを区別せず）受信時にプラン作成Podが起動されること
- [ ] Codex botからのレビューコメントでプラン作成Podが起動されること
- [ ] 通常のユーザーからのレビューコメントでプラン作成Podが起動されること
- [ ] `pr_review_comment`ではCodex bot判定が行われないこと
- [ ] プラン作成Podでwrite権限が剥奪されること
- [ ] プラン作成用プロンプトが正しく構築されること
- [ ] ReviewFeedbackの状態が正しく更新されること

---

### Phase 5: プラン実行完了時の処理

**目的**: プラン実行完了時にReviewFeedbackの状態を更新

**修正対象ファイル**:
- `internal/webhooks/handlers/agent_report.go`

**実装内容**:

1. **プラン実行完了時の処理追加**
   ```go
   // internal/webhooks/handlers/agent_report.go
   func HandleAgentReport(c *gin.Context) {
       // ... 既存の処理 ...

       // プラン実行モードの場合
       if agentRun.ExecutionMode == "plan_execution" {
           // ReviewFeedbackを更新
           if agentRun.ReviewFeedbackID != nil {
               reviewFeedbackRepo := repositories.NewReviewFeedbackRepository()
               reviewFeedback, err := reviewFeedbackRepo.FindByID(*agentRun.ReviewFeedbackID)
               if err == nil && reviewFeedback != nil {
                   if req.Status == "succeeded" {
                       // 成功時: 実行完了状態に遷移
                       reviewFeedback.PlanCreationStatus = "executed"
                       logger.Info("Plan execution completed successfully",
                           zap.Int("review_feedback_id", reviewFeedback.ID),
                           zap.Int("agent_run_id", agentRun.ID),
                       )
                   } else {
                       // 失敗時: created状態に戻す（再実行可能にする）
                       reviewFeedback.PlanCreationStatus = "created"
                       logger.Warn("Plan execution failed, reverting to 'created' state for retry",
                           zap.Int("review_feedback_id", reviewFeedback.ID),
                           zap.Int("agent_run_id", agentRun.ID),
                           zap.String("status", req.Status),
                           zap.String("error_message", req.ErrorMessage),
                       )
                       // 注意: 既存のリトライ機構により、AgentRunのStateが'failed'で
                       // リトライ上限に達していない場合は自動的に再実行される
                   }
                   reviewFeedbackRepo.Update(reviewFeedback)
               }
           }
       }

       // ... 既存の処理 ...
   }
   ```

**テスト項目**:
- [ ] プラン実行成功時にReviewFeedbackの状態が更新されること
- [ ] プラン実行失敗時に適切に処理されること

---

## 実装順序の推奨

### 推奨順序
1. **Phase 0** → **Phase 1** → **Phase 2** → **Phase 3** → **Phase 4** → **Phase 5**

### 理由
- Phase 0でapprove検出機能を移行し、既存機能への影響を最小化する
- Phase 1でデータモデルを拡張してから、各機能を実装する
- Phase 2でagent-runnerを拡張し、Phase 3でOperator APIを拡張する
- Phase 4でレビューフィードバック処理を拡張し、Phase 5で完了処理を追加する
- 各Phaseで段階的にテストを行い、問題を早期に発見する

---

## 工数見積もり

| Phase | 内容 | 開発工数 | テスト工数 |
|-------|------|----------|-----------|
| Phase 0 | Codex approve検出機能の移行 | 0.5 日 | 0.5 日 |
| Phase 1 | データモデル拡張 | 0.5 日 | 0.5 日 |
| Phase 2 | agent-runner拡張 | 1.5 日 | 1.0 日 |
| Phase 3 | Operator API拡張 | 1.0 日 | 1.0 日 |
| Phase 4 | レビューフィードバック処理拡張 | 1.0 日 | 1.0 日 |
| Phase 5 | プラン実行完了時の処理 | 0.5 日 | 0.5 日 |
| **合計** | | **5.0 日** | **4.5 日** |

**総工数**: 約 9.5 人日（開発 5.0 日 + テスト 4.5 日）

---

## リスクと対策

### リスク 1: プラン作成の品質
**問題**: AIが作成するプランが不適切な場合がある

**対策**:
- プラン作成プロンプトを詳細に設計し、明確な指示を出す
- プラン却下の条件を明確にする
- プラン実行前に人間がレビューできるようにする（将来的な拡張）

### リスク 2: プラン実行の失敗
**問題**: プランが作成されても実行に失敗する可能性がある

**対策**:
- プラン実行失敗時は既存のリトライ機構を利用
- プラン実行のログを詳細に記録
- 失敗時の通知機能を実装

### リスク 3: プラン作成Podのwrite権限剥奪
**問題**: write権限を剥奪しても、エージェントがファイルを変更しようとする可能性がある

**対策**:
- `CURSOR_ALLOW_WRITE=false`を環境変数で設定
- プロンプトで明確に「ファイル変更は行わない」と指示
- ファイル変更チェックをスキップする処理を実装

### リスク 4: プラン内容の解析
**問題**: AIの出力からプラン内容を正確に抽出する必要がある

**対策**:
- プラン出力の形式を明確に定義（例: "PLAN_CREATED\n\n[内容]"）
- パースエラー時のフォールバック処理を実装
- プラン内容の検証ロジックを追加

### リスク 5: 複数のレビューフィードバック
**問題**: 同じPRに複数のレビューフィードバックがある場合の処理

**対策**:
- 最新のレビューフィードバックのみを処理
- 既にプラン作成中の場合は新規起動をスキップ
- プラン作成状態を適切に管理

### リスク 6: approve検出の移行による既存機能への影響
**問題**: `pr_review_comment`から`issue_comment`への移行により、既存のapprove検出が動作しなくなる可能性

**対策**:
- Phase 0で移行を完了し、既存機能が正常に動作することを確認
- `issue_comment`でPRに関連するIssueかどうかを正確に判定
- 統合テストでapprove検出からマージまでのフローを検証

### リスク 7: 並行実行時の競合
**問題**: 同じPRに対して複数のレビューコメントが短時間に投稿された場合、複数のプラン作成Podが起動する可能性

**対策**:
- ReviewFeedbackのステータス更新時にデータベーストランザクションを使用
- または楽観的ロック（version カラム）を実装
- プラン作成中（`created`状態）の場合は新規Pod起動をスキップ
- コード例:
  ```go
  // トランザクション内でステータスチェックと更新を実施
  tx := db.Begin()
  defer tx.Rollback()

  reviewFeedback, err := reviewFeedbackRepo.FindByIDForUpdate(tx, reviewFeedbackID)
  if reviewFeedback.PlanCreationStatus != "pending" {
      // 既にプラン作成が開始されている場合はスキップ
      return nil
  }
  // プラン作成Pod起動...
  tx.Commit()
  ```

### リスク 8: 環境変数のサイズ制限
**問題**: プラン内容が大きい場合、環境変数（`PLAN_CONTENT`）のサイズ制限を超える可能性

**対策**:
- Kubernetesの環境変数サイズ制限: 1MB（ConfigMap/Secret経由でも同様）
- プラン内容が大きい場合はConfigMapとして作成し、ボリュームマウントで渡す
- または、プラン内容をファイルとして一時保存し、S3/オブジェクトストレージ経由で渡す
- プラン作成時にサイズチェックを実施し、閾値を超えた場合は警告
- コード例:
  ```go
  const maxPlanSize = 900 * 1024 // 900KB（余裕を持たせる）
  if len(planContent) > maxPlanSize {
      logger.Warn("Plan content exceeds size limit, using ConfigMap",
          zap.Int("plan_size", len(planContent)),
      )
      // ConfigMapを作成してマウントする処理
  }
  ```

### リスク 9: ログの機密情報漏洩
**問題**: プラン内容やエージェント出力にGitHub tokenやAPI keyが含まれる可能性

**対策**:
- プラン内容のログ出力時は100文字にプレビュー切り詰め（既存のcodex_approval.goパターンに準拠）
- エージェント出力のサニタイズ処理を実装
- トークンパターン（`ghp_`, `gho_`, etc.）を検出して`***`に置換
- コード例:
  ```go
  // agent-runner/pkg/reporter/client.go
  func sanitizeLogs(logs string) string {
      // GitHub token patterns
      patterns := []string{
          `ghp_[a-zA-Z0-9]{36}`,     // Personal access token
          `gho_[a-zA-Z0-9]{36}`,     // OAuth token
          `ghs_[a-zA-Z0-9]{36}`,     // Server token
          `sk-[a-zA-Z0-9]{48}`,      // OpenAI API key
          `ANTHROPIC_API_KEY.*`,     // Anthropic API key
      }
      sanitized := logs
      for _, pattern := range patterns {
          re := regexp.MustCompile(pattern)
          sanitized = re.ReplaceAllString(sanitized, "***")
      }
      return sanitized
  }

  func (c *Client) ReportPlanCreation(planContent, logs string) error {
      // プレビュー切り詰め
      preview := planContent
      if len(preview) > 100 {
          preview = preview[:100] + "..."
      }

      req := &PlanReportRequest{
          Status:      "plan_created",
          AgentType:   c.agentType,
          PlanContent: planContent,
          Logs:        sanitizeLogs(logs), // サニタイズ
      }
      return sendPlanReport(c, req)
  }
  ```

---

## 検証項目

### 機能テスト
- [ ] `issue_comment`でCodex approveが正しく検出されること
- [ ] `pr_review_comment`でapprove検出が行われないこと
- [ ] レビューフィードバック受信時にプラン作成Podが起動されること
- [ ] プラン作成Podでwrite権限が剥奪されること
- [ ] プランが正しく作成されること
- [ ] プランが却下される場合に適切に処理されること
- [ ] プラン作成完了時にプラン実行Podが自動起動されること
- [ ] プラン実行が正常に完了すること
- [ ] プラン却下時にPRにコメントが投稿されること

### 非機能テスト
- [ ] プラン作成Podの実行時間が適切であること
- [ ] プラン実行Podの実行時間が適切であること
- [ ] エラーハンドリングが適切であること
- [ ] ログが適切に記録されること

### 統合テスト
- [ ] レビューフィードバック受信からプラン作成・実行までのフローが正常に動作すること
- [ ] 複数のレビューフィードバックがある場合の処理が正常であること
- [ ] プラン実行失敗時のリトライが正常に動作すること

### 追加テスト項目（指摘事項対応）
- [ ] 並行実行時の競合テスト: 同時に複数のレビューコメントを投稿した場合、1つのプラン作成Podのみが起動されること
- [ ] 既存PR更新時のブランチ名検証テスト: レビュー対応時に既存PRのブランチが正しく使用されること
- [ ] 軽微なコメント（20文字未満）のフィルタリングテスト: 短いコメントがプラン作成をトリガーしないこと
- [ ] AgentType検出のラベルベーステスト: Issueラベルに応じて正しいAgentTypeが選択されること
- [ ] repoPartsバリデーションのエラーケーステスト: 不正なリポジトリ形式でエラーが正しくハンドリングされること
- [ ] プラン失敗時の状態復帰テスト: プラン実行失敗時に`created`状態に戻り、再実行可能であること
- [ ] 大きなプラン内容のサイズチェックテスト: 900KBを超えるプラン内容で警告が出ること
- [ ] ログサニタイズテスト: GitHub tokenやAPI keyがログに含まれないこと
- [ ] parsePlanResultのパースエラーテスト: 不正な形式の出力が却下として処理されること
- [ ] PLAN_CONTENT環境変数の検証テスト: プラン実行モード時に環境変数が正しく渡されること

---

## 参考資料

### 関連タスク
- `specs/001-github-agent-automation/tasks.md` - 関連タスクの定義
- `specs/001-github-agent-automation/contracts/ai-agent-execution.md` - エージェント実行契約

### 関連ファイル
- `internal/models/review_feedback.go` - ReviewFeedbackモデル
- `internal/models/agent_run.go` - AgentRunモデル
- `agent-runner/main.go` - agent-runnerメイン処理
- `internal/webhooks/handlers/issue_comment.go` - Issueコメントハンドラー（approve検出）
- `internal/webhooks/handlers/pr_review_comment.go` - レビューフィードバックハンドラー（指摘事項検出）
- `internal/webhooks/handlers/agent_report.go` - エージェントレポートハンドラー
- `internal/services/kubernetes_job.go` - Kubernetes Jobサービス
- `internal/services/codex_approval.go` - Codex approve検出サービス

---

## 実装チェックリスト

### Phase 0: Codex approve検出機能の移行
- [ ] `CodexApprovalDetector.IsCodexBot()`メソッドを追加
- [ ] `pr_review_comment`ハンドラーからapprove検出処理を削除
- [ ] `issue_comment`ハンドラーにapprove検出処理を追加
- [ ] PRに関連するIssueコメントの判定ロジックを実装
- [ ] ヘルパー関数（ciStatusProviderAdapter等）を移植
- [ ] ユニットテストを実装
- [ ] 統合テストを実装

### Phase 1: データモデル拡張
- [ ] ReviewFeedbackモデルにプラン関連フィールドを追加
- [ ] AgentRunモデルに実行モード関連フィールドを追加
- [ ] マイグレーションファイルを作成
- [ ] マイグレーションをテスト

### Phase 2: agent-runner拡張
- [ ] 実行モードフラグを追加
- [ ] プラン作成モードの処理を実装
- [ ] プラン実行モードの処理を実装
- [ ] プラン作成用プロンプト構築を実装
- [ ] プラン実行用プロンプト構築を実装
- [ ] プラン作成結果報告メソッドを実装
- [ ] ユニットテストを実装

### Phase 3: Operator API拡張
- [ ] プラン作成結果のリクエスト構造体を追加
- [ ] プラン作成結果処理を実装
- [ ] プラン実行Pod起動メソッドを実装
- [ ] ユニットテストを実装

### Phase 4: レビューフィードバック処理拡張
- [ ] プラン作成Pod起動ロジックを追加
- [ ] プラン作成Pod起動メソッドを実装
- [ ] JobConfig構造体を拡張
- [ ] ユニットテストを実装

### Phase 5: プラン実行完了時の処理
- [ ] プラン実行完了時の処理を実装
- [ ] ユニットテストを実装

### 統合テスト
- [ ] エンドツーエンドの統合テストを実装
- [ ] テスト環境で動作確認

### ドキュメント
- [ ] 実装内容をドキュメント化
- [ ] 運用手順をドキュメント化

---

## 連絡先・質問

実装中に不明点があれば以下を確認：
1. 本ドキュメント
2. 関連する契約ドキュメント
3. 既存コードのコメント
4. Issue または PR でディスカッション

