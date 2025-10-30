# quickstart.md: GitHub Agent Automation

## 必要権限
- GitHub: コラボレーター以上必須。
- Codex・Discord: APIトークン・Webhook要設定


## セットアップ
1. 本リポジトリをfork or clone
2. .env/GitHub Secrets等でAPIキーを設定（詳細後述）
3. コア依存パッケージのインストール：
   - `pnpm add express @octokit/webhooks @octokit/rest axios`
     - 利用パッケージ: express（REST API/Webhook）、@octokit/webhooks（GitHub Webhook）、@octokit/rest（GitHub REST API）、axios（外部API通知）
4. Webhook: GitHub（issue_comment, pull_request_review, check_suite, push, status）、Discord
5. `main` or featureブランチをcheckoutし、必要なエージェント/CIを有効化

## 基本運用例
- Issueへ「/run-agent」とコメント→AIエージェント実装自動開始
- PR上で「@codex review」コメント→Codex連携レビュー
- 変更反映はPR自動発行→Codex&CI通過で自動マージ
- 失敗/権限/依存タスクブロック等ステータスはGitHubコメントおよびDiscord通知で可視化

## FAQ
- 失敗・競合時は？
  - 指定回数（最大50）内で再試行、到達時はIssue/Discord即時通知
- 権限不足時や外部API失敗は？
  - ログをGitHub Issueに残しつつ即時Discord通知

## 成果物一覧チェック
- research.md: 仕様整理解説・未決課題リスト
- data-model.md: 全エンティティ・状態遷移・制約
- contracts/*.yaml: API自動連携契約

---
- agent-specificファイル（update-agent-context.sh等で同期可能）
- 必要に応じ、READMEや環境变量サンプルも配布可能
- 追加ファイル・不足点等は都度readmeに反映
