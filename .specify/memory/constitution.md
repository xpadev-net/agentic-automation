<!--
Sync Impact Report
- Version change: N/A → 1.0.0
- Modified principles: [template placeholders → concrete]
- Added sections: Additional Constraints, Development Workflow
- Removed sections: none
- Templates requiring updates:
  - ✅ .specify/templates/plan-template.md (Constitution Check gates align)
  - ✅ .specify/templates/spec-template.md (mandatory sections align)
  - N/A .specify/templates/commands/* (not present)
  - N/A README.md (not present)
- Follow-up TODOs: TODO(RATIFICATION_DATE): 初回採択日未確定
-->

# agentic-automation Constitution

## Core Principles

### I. Library-First
機能は独立したライブラリとして開始し、自己完結性と独立テスト可能性、明確な目的を
MUST 満たす。

- MUST: 各ライブラリは外部への隠蔽境界を持ち、副作用を最小化する
- MUST: 単体テスト・契約テストを独立実行できる構成にする
- MUST: 公開 API と責務をドキュメント化する
- SHOULD: 組織上の都合だけによるライブラリ分割は避ける

### II. CLI Interface
全ライブラリは CLI を通じて機能を公開し、テキスト I/O プロトコルを採用する。

- MUST: stdin/args → stdout、エラーは stderr に出力
- MUST: JSON と人間可読フォーマットの双方をサポート
- MUST: 非対話モードを前提とした安定した exit code 契約を提供
- SHOULD: サブコマンド設計とヘルプ出力を一貫させる

### III. Test-First (NON-NEGOTIABLE)
TDD を必須とし、Red-Green-Refactor を厳格に適用する。

- MUST: 受け入れ基準に基づくテストを先に作成し、承認を得る
- MUST: 失敗するテストを確認してから実装する
- MUST: 実装後にリファクタリング段階を設け、重複排除・設計改善を行う
- SHOULD: テストは観測可能な振る舞いに対して記述し、内部実装に依存しない

### IV. Integration Testing
契約変更や相互作用の要所に対して統合テストを課し、契約の堅牢性を保証する。

- MUST: 新規または変更された公開契約に対する契約テストを追加
- MUST: ライブラリ間通信・共有スキーマの互換性を検証
- SHOULD: 回帰の高リスク経路に優先度を置く

### V. Observability, Versioning & Simplicity
観測可能性を備え、SemVer に従ってバージョニングし、シンプルさを優先する。

- MUST: 構造化ログとテキスト I/O によりデバッグ容易性を確保
- MUST: 破壊的変更は MAJOR でのみ許容し、移行手段を提示
- MUST: バージョンは SemVer に準拠 (MAJOR.MINOR.PATCH)
- SHOULD: YAGNI に従い、必要最小限で設計・実装する

## Additional Constraints

セキュリティ、パフォーマンス、デプロイに関する最低限の拘束条件を定義する。

- Security: MUST 最小権限、依存関係の脆弱性スキャン、秘密情報の無出力
- Performance: SHOULD 主要パスの p95 目標を仕様化し、回帰監視を行う
- Deployment: MUST 冪等デプロイとロールバック手順を備える

## Development Workflow

開発フロー、レビュー、品質ゲート、承認プロセスを定義する。

- MUST: すべての PR でテスト成功と憲章遵守チェックを通過
- MUST: 設計に影響する変更は ADR または同等の記録を残す
- SHOULD: フィーチャーフラグによる段階的リリースを検討

## Governance

本憲章は開発プラクティスに優先し、改正は公開プロセスを通じて行う。

- Amendments: MUST PR ベースで提案し、議事録・影響分析・移行計画を含む
- Versioning Policy: MUST SemVer に準拠（MAJOR/ MINOR/ PATCH）
- Compliance: MUST 全 PR で憲章遵守レビューを実施

**Version**: 1.0.0 | **Ratified**: TODO(RATIFICATION_DATE): 初回採択日未確定 | **Last Amended**: 2025-10-30
