# "not implemented" スタブのクリーンアップ実装プラン

## 概要

完了済みタスク (T034, T036, T037) に関連する "not implemented" スタブを調査・整理し、適切に実装または削除する。

## 背景

### 現状の問題点
- タスク (T034, T036, T037) は tasks.md で完了済みとマークされているが、対応する関数が "not implemented" のままになっている
- 実際の機能は別ファイルに実装されている可能性があるが、スタブが残っていて混乱を招く
- 使用されていないコードがコードベースに残っている（メンテナンス負担）

### 影響を受けるファイル
1. `agent-runner/pkg/git/git.go:203` - `HasChanges()` (T036)
2. `agent-runner/pkg/lint/lint.go:8` - `RunLint()` (T034)
3. `agent-runner/pkg/lint/lint.go:14` - `RunTypeCheck()` (T034)
4. `agent-runner/pkg/context/context.go:18` - `LoadConfig()` (T037)
5. `agent-runner/pkg/context/context.go:24` - `BuildPrompt()` (T037)

## 実装計画

### Phase 1: 現状調査と分析

**目的**: 各スタブの状態を確認し、実装の必要性を判断する

**調査項目**:

1. **HasChanges() の調査** (`agent-runner/pkg/git/git.go:203`)
   - [ ] `diff.go` の `GetChangedFiles()` と `GetDiff()` の実装を確認
   - [ ] `HasChanges()` の呼び出し箇所を検索
   - [ ] 実際に使用されているかどうかを判定
   - [ ] 削除可能かどうかを判断

2. **RunLint() / RunTypeCheck() の調査** (`agent-runner/pkg/lint/lint.go`)
   - [ ] これらの関数の呼び出し箇所を検索
   - [ ] T034 の実装内容を `agent-runner/pkg/config/` で確認
   - [ ] `.agent-config.yaml` のローディング機能との関係を調査
   - [ ] Lint/TypeCheck 機能が他の場所に実装されているかを確認

3. **LoadConfig() / BuildPrompt() の調査** (`agent-runner/pkg/context/context.go`)
   - [ ] T037 の実装内容を調査（実装箇所を特定）
   - [ ] これらの関数の呼び出し箇所を検索
   - [ ] 実際の設定読み込み・プロンプト構築が他の場所にあるかを確認
   - [ ] 削除可能かどうかを判断

**成果物**:
- 各スタブの調査結果レポート（このドキュメントに追記）
- 削除 vs 実装の判断マトリクス

---

### Phase 2: 実装または削除の実行

**目的**: Phase 1 の調査結果に基づいて適切なアクションを実行

#### ケース A: スタブが使用されていない場合（推奨: 削除）

**実装内容**:
1. 該当関数を削除
2. インポートの整理
3. 関連するテストコードがあれば削除または修正

#### ケース B: 機能が他の場所に実装されている場合（推奨: 削除 + リダイレクト）

**例: HasChanges() の場合**
```go
// agent-runner/pkg/git/git.go
// HasChanges は削除し、diff.go の機能を直接使用するようにドキュメント化

// 呼び出し側を修正
// Before:
// hasChanges, err := git.HasChanges(workDir)

// After:
// changedFiles, err := git.GetChangedFiles(workDir)
// hasChanges := len(changedFiles) > 0
```

#### ケース C: 実装が本当に必要な場合（実装）

**例: RunLint() の場合（もし必要なら）**
```go
// agent-runner/pkg/lint/lint.go
func RunLint(workDir string) error {
    // .agent-config.yaml から lint コマンドを読み込む
    config, err := config.Load(workDir)
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }

    if config.Lint.Command == "" {
        return nil // lint コマンドが設定されていない場合はスキップ
    }

    cmd := exec.Command("sh", "-c", config.Lint.Command)
    cmd.Dir = workDir
    output, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("lint failed: %w\nOutput: %s", err, output)
    }

    return nil
}
```

---

### Phase 3: ドキュメント更新

**目的**: 変更内容をドキュメントに反映

**修正対象ファイル**:
- `specs/001-github-agent-automation/tasks.md`
- 該当ファイルのコメント
- 関連する契約ドキュメント（必要に応じて）

**実装内容**:

1. **tasks.md の更新**
   - T034, T036, T037 の説明を更新
   - 実装の最終状態を明記

2. **コードコメントの更新**
   - "Implementation will be added in T0XX" というコメントを削除
   - 必要に応じて新しいコメントを追加

3. **CLAUDE.md の更新（必要に応じて）**
   - Lint/TypeCheck の実行方法が変わった場合は反映

---

## 実装順序の推奨

### 推奨順序
1. **Phase 1** → **Phase 2** → **Phase 3**

### 理由
- まず調査を完全に行ってから削除/実装を判断する
- 一度に全てのスタブを処理するのではなく、ファイル単位で進める
- ドキュメント更新は最後にまとめて行う

---

## 調査結果（Phase 1 完了後に記入）

### HasChanges() (`agent-runner/pkg/git/git.go:200-204`)

**調査結果**:
- [x] 呼び出し箇所: `agent-runner/main.go:337` で使用されている
- [x] 代替実装: `pkg/git/diff.go` の `GetChangedFiles()` を使用可能
- [x] 判断: ☑ 実装

**理由**:
- `main.go` で実際に使用されているため、実装が必要
- `GetChangedFiles()` を呼び出して `len(changedFiles) > 0` で判定する実装を追加

**実装完了**: ✅ `GetChangedFiles()` を使用して実装完了

---

### RunLint() / RunTypeCheck() (`agent-runner/pkg/lint/lint.go`)

**調査結果**:
- [x] 呼び出し箇所: なし（使用されていない）
- [x] T034 の実装内容: `pkg/config/loader.go` で `.agent-config.yaml` の読み込み機能が実装済み。Lint/TypeCheck は Validation フックで実装済み
- [x] 判断: ☑ 削除

**理由**:
- 呼び出し箇所がなく、機能は `.agent-config.yaml` の Validation フックで実装済み
- スタブファイルのみのため削除

**削除完了**: ✅ `agent-runner/pkg/lint/lint.go` を削除

---

### LoadConfig() / BuildPrompt() (`agent-runner/pkg/context/context.go`)

**調査結果**:
- [x] 呼び出し箇所: 
  - `LoadConfig()`: なし（使用されていない）
  - `BuildPrompt()`: `agent-runner/main.go:290` で使用されている
- [x] T037 の実装内容: 
  - T037は `pkg/git/diff.go` の実装で完了済み
  - `main.go` では `validateEnv()` で直接環境変数を読み込んでいる
- [x] 判断: 
  - `LoadConfig()`: ☑ 削除
  - `BuildPrompt()`: ☑ 実装
  - `Config` 型: 保持（`reporter.Config` のエイリアスとして使用されているため）

**理由**:
- `LoadConfig()` は使用されておらず、`main.go` で直接環境変数を読み込んでいるため削除
- `BuildPrompt()` は `main.go` で使用されているため、`previousAttempts` と `ciLogs` を組み合わせて実装
- `Config` 型は `reporter.Config` のコメントで参照されているため保持

**実装/削除完了**: 
- ✅ `LoadConfig()` を削除
- ✅ `BuildPrompt()` を実装（`previousAttempts` と `ciLogs` を組み合わせてプロンプトを構築）
- ✅ `Config` 型を保持（後方互換性のため）

---

## 工数見積もり

| Phase | 内容 | 開発工数 | テスト工数 |
|-------|------|----------|-----------|
| Phase 1 | 調査と分析 | 0.5 日 | - |
| Phase 2 | 実装または削除 | 0.5 日 | 0.5 日 |
| Phase 3 | ドキュメント更新 | 0.25 日 | - |
| **合計** | | **1.25 日** | **0.5 日** |

**総工数**: 約 1.75 人日

---

## リスクと対策

### リスク 1: 削除すべきでない関数を削除してしまう
**問題**: 将来使用される予定の関数を削除してしまう可能性

**対策**:
- Git 履歴で関数の追加コミットを確認
- PR やコミットメッセージから意図を読み取る
- 不明な場合は削除せず、コメントで "DEPRECATED" とマーク

### リスク 2: 他の箇所に影響を与える
**問題**: スタブを削除することで、import エラーやビルドエラーが発生

**対策**:
- 削除前に必ず `grep` で呼び出し箇所を検索
- ビルドテストを実行
- CI が通ることを確認

### リスク 3: T034/T036/T037 の本来の実装箇所を見落とす
**問題**: 実装が別の場所にあるのに気づかず、二重実装してしまう

**対策**:
- タスク番号でコードベース全体を検索
- Git コミット履歴から T034/T036/T037 の実装を追跡
- 関連する PR を確認

---

## 検証項目

### コード品質
- [ ] 削除/変更後もビルドが通る
- [ ] 既存のテストが全てパスする
- [ ] `go vet` / `golangci-lint` のチェックがパスする
- [ ] 不要な import が残っていない

### 機能テスト（実装した場合）
- [ ] Lint 実行が正常に動作する（RunLint の場合）
- [ ] TypeCheck 実行が正常に動作する（RunTypeCheck の場合）
- [ ] 設定ファイルの読み込みが正常に動作する（LoadConfig の場合）
- [ ] プロンプト構築が正常に動作する（BuildPrompt の場合）
- [ ] 変更検知が正常に動作する（HasChanges の場合）

### ドキュメント
- [ ] tasks.md の記載が正確である
- [ ] コードコメントが適切に更新されている
- [ ] 削除した機能の代替方法がドキュメント化されている

---

## 参考資料

### 関連タスク
- `specs/001-github-agent-automation/tasks.md` - T034, T036, T037 の定義
- `specs/001-github-agent-automation/plan.md` - 全体設計

### 関連ファイル
- `agent-runner/pkg/git/git.go` - HasChanges スタブ
- `agent-runner/pkg/git/diff.go` - 変更検知の実装
- `agent-runner/pkg/lint/lint.go` - Lint/TypeCheck スタブ
- `agent-runner/pkg/context/context.go` - Config/Prompt スタブ
- `agent-runner/pkg/config/config.go` - 設定ファイル読み込み（T034）

---

## クリーンアップチェックリスト

### 調査
- [ ] HasChanges() の呼び出し箇所を特定
- [ ] RunLint() / RunTypeCheck() の呼び出し箇所を特定
- [ ] LoadConfig() / BuildPrompt() の呼び出し箇所を特定
- [ ] 各機能の代替実装を確認
- [ ] Git 履歴で追加経緯を確認

### 実装
- [ ] HasChanges() の処理完了
- [ ] RunLint() の処理完了
- [ ] RunTypeCheck() の処理完了
- [ ] LoadConfig() の処理完了
- [ ] BuildPrompt() の処理完了

### テスト
- [ ] ビルドテスト実施
- [ ] 既存テスト実行
- [ ] 新規実装のテスト作成（実装した場合）

### ドキュメント
- [ ] tasks.md 更新
- [ ] コードコメント更新
- [ ] CLAUDE.md 更新（必要に応じて）

---

## 連絡先・質問

実装中に不明点があれば以下を確認：
1. 本ドキュメント
2. tasks.md の該当タスク定義
3. Git コミット履歴（`git log --grep="T034\|T036\|T037"`）
4. Issue または PR でディスカッション
