package capabilities

import (
	"encoding/json"
)

// maxStoredResponseBytes 审计留档时最多存储的后端响应字节数。
// 仅影响留档快照(_raw)，不影响给 LLM 的 data 或后端读取上限。
const maxStoredResponseBytes = 8 * 1024

// maxNonJSONContentBytes 非 JSON 响应作为文本文档交给 LLM 时的内容长度上限。
// LLM token 预算有限，超出截断并在 fields 里标注 truncated。
const maxNonJSONContentBytes = 6000

// redactResponse 生成供审计留档的后端响应快照：
//   - 合法 JSON：递归剔除敏感字段键（任意层级）后重新序列化；
//   - 非 JSON（如纯文本/CSV/非法 JSON）：无法按键脱敏，仅做长度截断；
//   - 一律截断到 maxStoredResponseBytes。
func redactResponse(payload []byte) string {
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return truncatePayload(payload)
	}
	b, _ := json.Marshal(redactValue(v))
	return truncatePayload(b)
}

// redactValue 深拷贝值并剔除任意层级的敏感字段键（map 键命中即跳过整棵子树）。
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isSensitive(k) {
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = redactValue(t[i])
		}
		return t
	default:
		return v
	}
}

// BackendError 携带后端失败的脱敏原始响应体，供审计留档。
// Error() 只返回短消息、绝不包含 body，避免把原始响应污染进常规错误展示。
type BackendError struct {
	Err          error
	StatusCode   int
	BodyRedacted string
}

func (e *BackendError) Error() string { return e.Err.Error() }
func (e *BackendError) Unwrap() error { return e.Err }

// newBackendError 构造携带脱敏响应体的后端错误。
func newBackendError(err error, statusCode int, rawBody []byte) error {
	return &BackendError{Err: err, StatusCode: statusCode, BodyRedacted: redactResponse(rawBody)}
}

// topLevelArray 尝试把 payload 解析为顶层 JSON 数组（非对象、非文本）。
func topLevelArray(payload []byte) ([]any, bool) {
	var arr []any
	if json.Unmarshal(payload, &arr) == nil && arr != nil {
		return arr, true
	}
	return nil, false
}

func truncatePayload(p []byte) string {
	if len(p) <= maxStoredResponseBytes {
		return string(p)
	}
	return string(p[:maxStoredResponseBytes-1]) + "…"
}
