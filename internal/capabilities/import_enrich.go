package capabilities

import "context"

// ImportEnricher 在导入器从 OpenAPI 解析出规则草稿后，对草稿做补充加工（例如用 LLM
// 补 input_schema 字段的中文说明/示例/枚举、优化 ai.description 与领域推断），
// 让用户少补几步。可为 nil（不启用，走原始规则草稿）。
type ImportEnricher interface {
	// Enrich 返回加工后的草稿副本。实现必须幂等、容错：LLM 失败时返回输入本身
	// （或仅成功部分），不能因富化失败而让整个导入中断。
	Enrich(ctx context.Context, drafts []Capability) ([]Capability, error)
}

// nopEnricher 是不做任何加工的默认实现。
type nopEnricher struct{}

func (nopEnricher) Enrich(_ context.Context, drafts []Capability) ([]Capability, error) {
	return drafts, nil
}
