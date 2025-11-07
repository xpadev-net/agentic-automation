# レビュー指摘対応のプラン作成・実行機能実装プラン

## 概要

レビューフィードバックで`approval_detected=false`の場合、以下の2段階の処理を実装します：
1. **プラン作成Pod**: 指摘事項を検討し、プランを作成または却下
2. **プラン実行Pod**: プランが作成された場合、そのプランを実行

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
       PlanCreationStatus string    `gorm:"type:enum('pending','plan_created','plan_rejected','plan_executing','plan_executed');default:'pending'"`
       PlanContent        *string   `gorm:"type:text"` // プラン内容
       PlanAgentRunID     *int      `gorm:"column:plan_agent_run_id;index"` // プラン作成用AgentRun ID
       ExecutionAgentRunID *int     `gorm:"column:execution_agent_run_id;index"` // プラン実行用AgentRun ID
   }
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
   ALTER TABLE review_feedback
       ADD COLUMN plan_creation_status ENUM('pending','plan_created','plan_rejected','plan_executing','plan_executed') DEFAULT 'pending',
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
   func Run(issueID int, repo, prompt, previousAttempts, ciLogs, executionMode string) error {
       // ... 既存の処理 ...
       
       // プラン作成モードの場合
       if executionMode == "plan_creation" {
           // write権限を剥奪（CURSOR_ALLOW_WRITE=false）
           envCfg.CursorAllowWrite = false
           
           // エージェント実行
           agentOutput, err := executor.Execute(envCfg.WorkDir, fullPrompt)
           
           // ファイル変更チェックをスキップ
           // コミット/PR作成をスキップ
           
           // プラン作成結果を報告
           // プランが作成されたか、却下されたかを判定
           planContent, rejected, rejectionReason := parsePlanResult(agentOutput)
           if rejected {
               return reporterClient.ReportPlanRejection(rejectionReason, agentOutput)
           }
           return reporterClient.ReportPlanCreation(planContent, agentOutput)
       }
       
       // プラン実行モードの場合
       if executionMode == "plan_execution" {
           // プラン内容をプロンプトに含める
           fullPrompt = buildPlanExecutionPrompt(prompt, planContent)
           // 通常の実行フローを続行
       }
       
       // ... 既存の処理 ...
   }
   ```

3. **プラン作成用プロンプト構築**
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

4. **プラン実行用プロンプト構築**
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

5. **プラン作成結果報告メソッドの追加**
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
           reviewFeedback.PlanCreationStatus = "plan_created"
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
           branchName := fmt.Sprintf("feature/issue-%d", issue.Number)
           job, err := jobService.CreateJobForPlanExecution(ctx, executionAgentRun, issue, req.PlanContent, branchName)
           if err != nil {
               // エラー処理
           }
           
           reviewFeedback.ExecutionAgentRunID = &executionAgentRun.ID
           reviewFeedback.PlanCreationStatus = "plan_executing"
           reviewFeedbackRepo.Update(reviewFeedback)
           
           c.JSON(http.StatusOK, gin.H{
               "message": "Plan created and execution job started",
               "agent_run_id": executionAgentRun.ID,
           })
       } else if req.Status == "plan_rejected" {
           // プラン却下時はPRにコメントを投稿
           reviewFeedback.PlanCreationStatus = "plan_rejected"
           reviewFeedbackRepo.Update(reviewFeedback)
           
           // PRにコメントを投稿
           pr, _ := repositories.NewPullRequestRepository().FindByID(reviewFeedback.PRID)
           repoParts := strings.Split(pr.Repo, "/")
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

**修正対象ファイル**:
- `internal/webhooks/handlers/pr_review_comment.go`
- `internal/services/kubernetes_job.go`

**実装内容**:

1. **プラン作成Pod起動ロジックの追加**
   ```go
   // internal/webhooks/handlers/pr_review_comment.go
   // HandlePullRequestReviewCommentWithDeps内で、承認検出後
   
   // 承認が検出されなかった場合（approval_detected=false）
   if !detector.DetectApproval(payload.Comment.Body, payload.Comment.User.Login, payload.Comment.User.ID) {
       // ReviewFeedbackを作成または更新
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
           // 新規作成
           reviewFeedback, _ = reviewFeedbackRepo.CreateReceivedReview(pr.ID, payload.Comment.Body, false, &commentID)
       }
       
       // プラン作成Podを起動
       if reviewFeedback != nil && reviewFeedback.PlanCreationStatus == "pending" {
           // Issueを取得
           issueRepo := repositories.NewIssueRepository()
           issue, err := issueRepo.FindByID(*pr.IssueID)
           if err != nil {
               logger.Error("Failed to get Issue for plan creation", zap.Error(err))
               return
           }
           
           // AgentRunを作成
           agentRunRepo := repositories.NewAgentRunRepository(config.GetDB())
           agentRun := &models.AgentRun{
               IssueID:          issue.Number,
               PRID:             &pr.ID,
               AgentType:        "claude-code", // デフォルト、または設定から取得
               ExecutionMode:    "plan_creation",
               ReviewFeedbackID: &reviewFeedback.ID,
               State:            "queued",
           }
           agentRunRepo.Create(agentRun)
           
           // プラン作成Podを起動
           kubernetesClient := clients.NewKubernetesClient(logger)
           jobService := services.NewKubernetesJobService(kubernetesClient, logger)
           branchName := fmt.Sprintf("feature/issue-%d", issue.Number)
           
           job, err := jobService.CreateJobForPlanCreation(ctx, agentRun, issue, reviewFeedback, branchName)
           if err != nil {
               logger.Error("Failed to create plan creation job", zap.Error(err))
               return
           }
           
           // ReviewFeedbackを更新
           reviewFeedback.PlanCreationStatus = "pending"
           reviewFeedback.PlanAgentRunID = &agentRun.ID
           reviewFeedbackRepo.Update(reviewFeedback)
           
           logger.Info("Plan creation job started",
               zap.Int("agent_run_id", agentRun.ID),
               zap.Int("review_feedback_id", reviewFeedback.ID),
           )
       }
   }
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
       // プラン作成用プロンプトを構築
       prompt := context.BuildPlanCreationPrompt(*reviewFeedback.Content)
       
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
           ExecutionMode:    "plan_creation", // 新規追加
           CursorAllowWrite: false,           // write権限を剥奪
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
       ExecutionMode    string // 新規追加: "normal", "plan_creation", "plan_execution"
       PlanContent      string // 新規追加: プラン実行モード時に使用
       CursorAllowWrite bool   // 新規追加: cursor-agentのwrite権限制御
   }
   ```

**テスト項目**:
- [ ] レビューフィードバック受信時にプラン作成Podが起動されること
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
                       reviewFeedback.PlanCreationStatus = "plan_executed"
                   } else {
                       // 失敗時は状態を保持（再実行可能にする）
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
1. **Phase 1** → **Phase 2** → **Phase 3** → **Phase 4** → **Phase 5**

### 理由
- Phase 1でデータモデルを拡張してから、各機能を実装する
- Phase 2でagent-runnerを拡張し、Phase 3でOperator APIを拡張する
- Phase 4でレビューフィードバック処理を拡張し、Phase 5で完了処理を追加する
- 各Phaseで段階的にテストを行い、問題を早期に発見する

---

## 工数見積もり

| Phase | 内容 | 開発工数 | テスト工数 |
|-------|------|----------|-----------|
| Phase 1 | データモデル拡張 | 0.5 日 | 0.5 日 |
| Phase 2 | agent-runner拡張 | 1.5 日 | 1.0 日 |
| Phase 3 | Operator API拡張 | 1.0 日 | 1.0 日 |
| Phase 4 | レビューフィードバック処理拡張 | 1.0 日 | 1.0 日 |
| Phase 5 | プラン実行完了時の処理 | 0.5 日 | 0.5 日 |
| **合計** | | **4.5 日** | **4.0 日** |

**総工数**: 約 8.5 人日（開発 4.5 日 + テスト 4.0 日）

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

---

## 検証項目

### 機能テスト
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

---

## 参考資料

### 関連タスク
- `specs/001-github-agent-automation/tasks.md` - 関連タスクの定義
- `specs/001-github-agent-automation/contracts/ai-agent-execution.md` - エージェント実行契約

### 関連ファイル
- `internal/models/review_feedback.go` - ReviewFeedbackモデル
- `internal/models/agent_run.go` - AgentRunモデル
- `agent-runner/main.go` - agent-runnerメイン処理
- `internal/webhooks/handlers/pr_review_comment.go` - レビューフィードバックハンドラー
- `internal/webhooks/handlers/agent_report.go` - エージェントレポートハンドラー
- `internal/services/kubernetes_job.go` - Kubernetes Jobサービス

---

## 実装チェックリスト

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

