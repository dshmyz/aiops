package assistant

import (
	"fmt"
	"path/filepath"
	"strings"
)

// 附件（日志/文本文件随消息发送）约束。
// 传输侧硬上限由 httpapi 的 assistantBodyLimit 把守；这里做精确校验。
const (
	// MaxAttachmentsPerMessage 单条消息最多携带的附件数。运维排障场景一次
	// 贴几个日志足够，超出的让用户合并或挑选关键片段。
	MaxAttachmentsPerMessage = 5
	// MaxAttachmentBytes 单个附件解码后的 UTF-8 字节上限（400KB ≈ 13 万汉字）。
	MaxAttachmentBytes = 400 << 10
	// maxTotalAttachmentBytes 全部附件合计字节上限。必须小于单限×数量
	//（1MB < 5×400KB）才有约束力：防"多个中型日志凑一起"挤爆 prompt。
	maxTotalAttachmentBytes = 1 << 20
	// MaxAttachmentPromptRunes 注入 prompt 时每个附件保留的最大字符数（rune），
	// 服务端统一截断并在块头标注，避免单个大日志吞掉整轮上下文预算。
	MaxAttachmentPromptRunes = 8000
)

// allowedAttachmentExts 文本类扩展名白名单。空扩展名放行（容器/设备日志常无后缀），
// 二进制行为由 NUL 字节探测兜底拒绝。
var allowedAttachmentExts = map[string]bool{
	".log": true, ".txt": true, ".text": true, ".json": true,
	".yaml": true, ".yml": true, ".xml": true, ".csv": true,
	".conf": true, ".ini": true, ".properties": true, ".out": true,
}

// Attachment 是用户随消息附带的一个文本文件（前端读全文后内联传输）。
type Attachment struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// ValidateAttachments 校验附件数量、命名、大小、类型与二进制内容。
// 返回的错误文案直接面向用户展示（router 以 400 原样透传），保持中文、可操作。
func ValidateAttachments(attachments []Attachment) error {
	if len(attachments) == 0 {
		return nil
	}
	if len(attachments) > MaxAttachmentsPerMessage {
		return fmt.Errorf("附件数量超过上限（最多 %d 个）", MaxAttachmentsPerMessage)
	}
	total := 0
	for _, att := range attachments {
		name := strings.TrimSpace(att.Name)
		switch {
		case name == "":
			return fmt.Errorf("附件名不能为空")
		case len([]rune(name)) > 255:
			return fmt.Errorf("附件名过长（%s…）", truncateRunesStr(name, 20))
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != "" && !allowedAttachmentExts[ext] {
			return fmt.Errorf("暂不支持的文件类型 %s（%s）：请粘贴为文本或转存为 .txt", ext, name)
		}
		size := len(att.Content)
		total += size
		if size > MaxAttachmentBytes {
			return fmt.Errorf("附件 %s 超过大小上限（最大 %d KB，实际 %d KB）",
				name, MaxAttachmentBytes>>10, size>>10)
		}
		// 二进制探测：文本文件几乎不会出现 NUL 字节（UTF-16 BOM、gzip 头等都命中）。
		head := att.Content
		if len(head) > 8192 {
			head = head[:8192]
		}
		if strings.IndexByte(head, 0) >= 0 {
			return fmt.Errorf("附件 %s 疑似二进制文件，仅支持文本内容", name)
		}
	}
	if total > maxTotalAttachmentBytes {
		return fmt.Errorf("附件总大小超过上限（最大 %d MB）", maxTotalAttachmentBytes>>20)
	}
	return nil
}

// FormatAttachmentsForPrompt 把附件渲染成追加在用户消息后的 markdown 编码块序列。
// 无附件时返回空串。每个附件：
//   - 块头声明文件名与字符规模，截断时显式标注；
//   - 围栏长度取 max(3, 内容内最长反引号串+1)，防止日志自身含 ``` 击穿围栏。
//
// 截断在服务端执行（而非前端），保证「所见即所发」的反面成立时用户也能从回复中
// 得知模型只看到了前缀——块头的"已截断"标注就是为此服务的。
func FormatAttachmentsForPrompt(attachments []Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	var b strings.Builder
	for i, att := range attachments {
		if i > 0 {
			b.WriteString("\n\n")
		}
		content := att.Content
		total := len([]rune(content))
		header := fmt.Sprintf("[附加文件 %s（%d 字符）]", att.Name, total)
		if total > MaxAttachmentPromptRunes {
			content = truncateRunesStr(content, MaxAttachmentPromptRunes)
			header = fmt.Sprintf("[附加文件 %s（已截断：仅含前 %d 字符，共 %d 字符）]",
				att.Name, MaxAttachmentPromptRunes, total)
		}
		b.WriteString(header)
		b.WriteString("\n")
		fence := fenceFor(content)
		b.WriteString(fence)
		if lang := extAsLang(att.Name); lang != "" {
			b.WriteString(lang)
		}
		b.WriteString("\n")
		b.WriteString(content)
		b.WriteString("\n")
		b.WriteString(fence)
	}
	return b.String()
}

// ComposeMessageWithAttachments 组合用户正文与附件块为最终发给 LLM 并落库的消息文本。
// 正文原样在前，附件块以空行分隔追加在后。
func ComposeMessageWithAttachments(message string, attachments []Attachment) string {
	block := FormatAttachmentsForPrompt(attachments)
	if block == "" {
		return message
	}
	return message + "\n\n" + block
}

// fenceFor 计算能包裹 content 的最小安全围栏：内容里最长反引号串 + 1，至少 3 个。
func fenceFor(content string) string {
	longest, cur := 0, 0
	for _, r := range content {
		if r == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

// extAsLang 从文件名推断围栏语言标记（仅作为高亮提示，不影响解析）。
func extAsLang(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "text" || ext == "conf" || ext == "ini" || ext == "properties" || ext == "out" {
		return ""
	}
	return ext
}

// truncateRunesStr 按 rune 截断字符串（与按字节 truncate 不同，保证不切出半个字）。
func truncateRunesStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
