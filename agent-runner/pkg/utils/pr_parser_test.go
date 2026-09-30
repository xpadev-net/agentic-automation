package utils

import (
	"strings"
	"testing"
)

// Codex-style output: the CLI echoes the prompt (which itself contains the
// example output format) before printing the agent's answer as plain text.
func TestParsePRTitleAndBody_PlainTextWithPromptEcho(t *testing.T) {
	output := `user
以下の情報を基に、Pull Requestのタイトルと概要を生成してください。
出力形式:
以下のXML形式で出力してください。
<title>PRタイトル</title>
<body>PR概要（Markdown形式可）</body>

codex
<title>feat: add codex support</title>
<body>実際のPR本文</body>
`
	title, body, err := ParsePRTitleAndBody(output, OutputPlainText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "feat: add codex support" {
		t.Errorf("expected last <title> match, got %q", title)
	}
	if body != "実際のPR本文" {
		t.Errorf("expected last <body> match, got %q", body)
	}
}

func TestParseCommitMessage_PlainTextWithPromptEcho(t *testing.T) {
	output := `user
<commit_message>コミットメッセージ（必ずIssue番号 #1 を含めてください）</commit_message>
と出力してください。

codex
<commit_message>feat: add codex agent (#1)</commit_message>
`
	msg, err := ParseCommitMessage(output, OutputPlainText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "feat: add codex agent (#1)" {
		t.Errorf("expected last <commit_message> match, got %q", msg)
	}
}

func TestParsePRTitleAndBody_StreamJSON(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"<title>json title</title>\n<body>json body</body>"}]}}
{"type":"result","subtype":"success"}`
	title, body, err := ParsePRTitleAndBody(output, OutputCursorJSONL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "json title" {
		t.Errorf("expected title from assistant entry, got %q", title)
	}
	if body != "json body" {
		t.Errorf("expected body from assistant entry, got %q", body)
	}
}

func TestParsePRTitleAndBody_CodexJSONL(t *testing.T) {
	output := `{"type":"thread.started","thread_id":"thread-1"}
{"type":"item.completed","item":{"id":"item-1","type":"reasoning","text":"internal"}}
{"type":"item.completed","item":{"id":"item-2","type":"agent_message","text":"検討結果です。"}}
{"type":"item.completed","item":{"id":"item-3","type":"agent_message","text":"<title>docs: \"quoted\" title</title>\n<body>first line\nsecond line</body>"}}
{"type":"turn.completed"}`

	title, body, err := ParsePRTitleAndBody(output, OutputCodexJSONL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != `docs: "quoted" title` {
		t.Errorf("expected decoded title, got %q", title)
	}
	if body != "first line\nsecond line" {
		t.Errorf("expected decoded multiline body, got %q", body)
	}
}

func TestParseCommitMessage_CodexJSONL(t *testing.T) {
	output := `{"type":"item.started","item":{"type":"agent_message"}}
{"type":"item.completed","item":{"type":"agent_message","text":"前置き"}}
{"type":"item.completed","item":{"type":"agent_message","text":"<commit_message>docs: explain \"Codex\" output (#304)</commit_message>"}}
{"type":"turn.completed"}`

	message, err := ParseCommitMessage(output, OutputCodexJSONL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if message != `docs: explain "Codex" output (#304)` {
		t.Errorf("expected decoded commit message, got %q", message)
	}
}

func TestParsePRTitleAndBody_TitleTagInsideBody(t *testing.T) {
	// A literal <title> mention inside the markdown body must not shadow the real title.
	output := `<title>real title</title>
<body>本文中に <title>例示</title> という文字列を含む</body>`
	title, body, err := ParsePRTitleAndBody(output, OutputPlainText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "real title" {
		t.Errorf("expected title before last body, got %q", title)
	}
	if body != "本文中に <title>例示</title> という文字列を含む" {
		t.Errorf("expected last body match, got %q", body)
	}
}

func TestParsePRTitleAndBody_BodyBeforeTitleWithEcho(t *testing.T) {
	// Answer emitted in <body>-then-<title> order after a prompt echo: the
	// answer's title follows the last body, so the echo's example title must
	// not be selected.
	output := `user
<title>PRタイトル</title>
<body>PR概要（Markdown形式可）</body>

codex
<body>実際のPR本文</body>
<title>feat: real title</title>
`
	title, body, err := ParsePRTitleAndBody(output, OutputPlainText)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "feat: real title" {
		t.Errorf("expected trailing title, got %q", title)
	}
	if body != "実際のPR本文" {
		t.Errorf("expected last body, got %q", body)
	}
}

func TestParsePRTitleAndBody_NoTags(t *testing.T) {
	_, _, err := ParsePRTitleAndBody("no xml tags here", OutputPlainText)
	if err == nil || !strings.Contains(err.Error(), "title tag not found") {
		t.Fatalf("expected title tag error, got %v", err)
	}
}
