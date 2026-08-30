package assistant

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateAttachmentsAcceptsTextFiles(t *testing.T) {
	atts := []Attachment{
		{Name: "app.log", Content: "2026-08-27 INFO started"},
		{Name: "config.yaml", Content: "replicas: 3\n"},
		{Name: "kubelet", Content: "I0827 ts=... msg=ok"}, // 无扩展名
	}
	if err := ValidateAttachments(atts); err != nil {
		t.Fatalf("ValidateAttachments() error = %v, want nil", err)
	}
}

func TestValidateAttachmentsNilAndEmpty(t *testing.T) {
	for _, atts := range [][]Attachment{nil, {}} {
		if err := ValidateAttachments(atts); err != nil {
			t.Fatalf("ValidateAttachments(%v) error = %v, want nil", atts, err)
		}
	}
}

func TestValidateAttachmentsRejectsTooMany(t *testing.T) {
	atts := make([]Attachment, MaxAttachmentsPerMessage+1)
	for i := range atts {
		atts[i] = Attachment{Name: "a.log", Content: "x"}
	}
	err := ValidateAttachments(atts)
	if err == nil || !strings.Contains(err.Error(), "数量超过上限") {
		t.Fatalf("err = %v, want 数量超限", err)
	}
}

func TestValidateAttachmentsRejectsEmptyName(t *testing.T) {
	err := ValidateAttachments([]Attachment{{Name: "  ", Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), "附件名不能为空") {
		t.Fatalf("err = %v, want empty name error", err)
	}
}

func TestValidateAttachmentsRejectsDisallowedExt(t *testing.T) {
	err := ValidateAttachments([]Attachment{{Name: "dump.bin", Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), ".bin") {
		t.Fatalf("err = %v, want .bin rejected", err)
	}
}

func TestValidateAttachmentsRejectsOversize(t *testing.T) {
	big := strings.Repeat("x", MaxAttachmentBytes+1)
	err := ValidateAttachments([]Attachment{{Name: "big.log", Content: big}})
	if err == nil || !strings.Contains(err.Error(), "big.log") {
		t.Fatalf("err = %v, want oversize rejected with name", err)
	}
}

func TestValidateAttachmentsRejectsBinary(t *testing.T) {
	var b strings.Builder
	b.WriteString("\x1f\x8b\x08\x00gzip-ish")
	err := ValidateAttachments([]Attachment{{Name: "log.gz.txt", Content: b.String()}})
	if err == nil || !strings.Contains(err.Error(), "二进制") {
		t.Fatalf("err = %v, want binary rejected", err)
	}
}

func TestValidateAttachmentsRejectsTotalOverBudget(t *testing.T) {
	// 单个 350KB（低于 400KB 单限），三个合计 1050KB 超过 1MB 总限——
	// 只有这样才验证的是总量规则而不是单文件规则被误触发。
	part := strings.Repeat("x", 350<<10)
	atts := []Attachment{
		{Name: "a.log", Content: part},
		{Name: "b.log", Content: part},
		{Name: "c.log", Content: part},
	}
	err := ValidateAttachments(atts)
	if err == nil || !strings.Contains(err.Error(), "总大小") {
		t.Fatalf("err = %v, want total budget error", err)
	}
}

func TestFormatAttachmentsForPromptEmpty(t *testing.T) {
	if got := FormatAttachmentsForPrompt(nil); got != "" {
		t.Fatalf("FormatAttachmentsForPrompt(nil) = %q, want empty", got)
	}
}

func TestFormatAttachmentsForPromptWrapsFencedBlock(t *testing.T) {
	out := FormatAttachmentsForPrompt([]Attachment{
		{Name: "app.log", Content: "line1\nline2"},
	})
	// "line1\nline2" = 11 字符
	if !strings.Contains(out, "[附加文件 app.log（11 字符）]") {
		t.Fatalf("missing header with char count, out = %q", out)
	}
	if !strings.Contains(out, "```log\nline1\nline2\n```") {
		t.Fatalf("missing fenced block with lang, out = %q", out)
	}
}

func TestFormatAttachmentsForPromptTruncatesLongContent(t *testing.T) {
	content := strings.Repeat("x", MaxAttachmentPromptRunes+5000)
	out := FormatAttachmentsForPrompt([]Attachment{{Name: "huge.log", Content: content}})
	if !strings.Contains(out, "已截断") {
		t.Fatalf("expected truncation notice in header, out prefix = %q", out[:80])
	}
	if !strings.Contains(out, fmt.Sprintf("%d", MaxAttachmentPromptRunes)) {
		t.Fatalf("header should carry truncated rune count %d", MaxAttachmentPromptRunes)
	}
	// 内容应只保留前 8000 字符
	if got := strings.Count(out, "x"); got != MaxAttachmentPromptRunes {
		t.Fatalf("content runes = %d, want %d", got, MaxAttachmentPromptRunes)
	}
}

func TestFormatAttachmentsForPromptEscapesBacktickFences(t *testing.T) {
	// 日志内容自带 ``` 围栏时，外层围栏必须加长，否则结构被击穿。
	content := "before\n```\ninner fence\n```\nafter"
	out := FormatAttachmentsForPrompt([]Attachment{{Name: "tricky.log", Content: content}})
	if !strings.Contains(out, "````log") {
		t.Fatalf("outer fence not widened, out = %q", out)
	}
}

func TestComposeMessageWithAttachmentsPlain(t *testing.T) {
	got := ComposeMessageWithAttachments("检查集群健康", nil)
	if got != "检查集群健康" {
		t.Fatalf("got %q, want unchanged message", got)
	}
	got = ComposeMessageWithAttachments("看这个日志", []Attachment{{Name: "a.log", Content: "boom"}})
	wantPrefix := "看这个日志\n\n[附加文件 a.log（4 字符）]"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("got %q, want prefix %q", got, wantPrefix)
	}
}
