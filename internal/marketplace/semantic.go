package marketplace

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/knowledge"
)

// ErrSemanticUnavailable is returned by SemanticSearch when the marketplace was
// not wired with a knowledge store/embedder. The HTTP layer maps it to 503.
var ErrSemanticUnavailable = errors.New("semantic search is not configured")

// capDocIDPrefix 是能力语义文档在 knowledge store 里的 ID 前缀。文档 ID 用它
// 前缀 + 能力 registry ID 组成，保证可逆（从文档 ID 找回能力 ID）且可重入
// （重新发布同一能力不会在 store 里堆积重复文档）。
const capDocIDPrefix = "capability:"

// EnableSemantic 给 marketplace 接入可选的语义检索。store 为 nil 时再调用
// SemanticSearch 会返回 ErrSemanticUnavailable；embedder 可为 nil（此时退化为
// knowledge store 的子串检索，离线可用）。
func (s *Service) EnableSemantic(store knowledge.Store, embedder knowledge.Embedder) {
	s.semStore = store
	s.semEmbed = embedder
}

// SemanticSearch 用自然语言查询召回能力：把查询文本嵌入后按余弦相似度在
// 知识库里检索，命中带回 registry 详情。只返回 published 且未弃用的能力，
// 与关键词 Search 的默认语义一致。
func (s *Service) SemanticSearch(ctx context.Context, query string, topK, limit int) ([]Registry, error) {
	if s.semStore == nil {
		return nil, ErrSemanticUnavailable
	}
	if topK <= 0 {
		topK = 10
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var docs []knowledge.Document
	if s.semEmbed != nil {
		if vs, ok := s.semStore.(knowledge.VectorRetrieverStore); ok {
			vec, err := s.semEmbed.Embed(ctx, query)
			if err == nil && len(vec) > 0 {
				if retrieved, err := vs.RetrieveByVector(ctx, vec, topK); err == nil && len(retrieved) > 0 {
					docs = retrieved
				}
			}
		}
	}
	// 无 embedder，或向量检索没结果时退化为子串检索。
	if len(docs) == 0 {
		var err error
		docs, err = s.semStore.Retrieve(ctx, query, topK)
		if err != nil {
			return nil, fmt.Errorf("semantic retrieve: %w", err)
		}
	}

	out := make([]Registry, 0, limit)
	for _, doc := range docs {
		if doc.Source != SourceCapabilitySemantic {
			continue
		}
		if !strings.HasPrefix(doc.ID, capDocIDPrefix) {
			continue
		}
		id := strings.TrimPrefix(doc.ID, capDocIDPrefix)
		if id == "" {
			continue
		}
		reg, err := s.Get(ctx, id)
		if err != nil {
			// 索引指向的能力已被删除：跳过，检索不因脏索引而失败。
			continue
		}
		if reg.Status != StatusPublished {
			continue
		}
		out = append(out, *reg)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// SourceCapabilitySemantic 标注知识库里来自能力市场的文档，便于检索过滤。
const SourceCapabilitySemantic = "capability-marketplace"

// indexCapability 把一条能力的语义描述建成 knowledge 文档。文档内容混入
// 名称、domain、operation、AI 描述与示例，让"我想重启 Kafka"这类自然语言
// 命中 kafka.broker.restart。索引失败只记日志不报错（best-effort）。
func (s *Service) indexCapability(ctx context.Context, reg *Registry, cap capabilities.Capability) error {
	if reg == nil || s.semStore == nil {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("工具名：")
	sb.WriteString(reg.Name)
	sb.WriteString("\ndomain：")
	sb.WriteString(reg.Domain)
	sb.WriteString("\n操作类型：")
	sb.WriteString(string(cap.Operation))
	if desc := strings.TrimSpace(cap.AI.Description); desc != "" {
		sb.WriteString("\n描述：")
		sb.WriteString(desc)
	}
	if len(cap.AI.Examples) > 0 {
		sb.WriteString("\n示例：")
		sb.WriteString(strings.Join(cap.AI.Examples, "；"))
	}

	doc := knowledge.EmbeddedDocument{
		Document: knowledge.Document{
			ID:        capDocIDPrefix + reg.ID,
			Title:     reg.Name,
			Content:   sb.String(),
			Source:    SourceCapabilitySemantic,
			CreatedAt: s.now(),
		},
	}
	if s.semEmbed != nil {
		if vec, err := s.semEmbed.Embed(ctx, doc.Content); err == nil {
			doc.Embedding = vec
		}
	}
	// 先删后插，保证同一能力只有一个文档（可重入）。
	if rem, ok := s.semStore.(knowledge.Remover); ok {
		_ = rem.Remove(ctx, doc.ID)
	}
	_ = s.semStore.Add(ctx, doc)
	return nil
}

// removeCapabilityIndex 在能力被弃用/删除时移出语义检索。
func (s *Service) removeCapabilityIndex(ctx context.Context, capabilityID string) error {
	if s.semStore == nil {
		return nil
	}
	if rem, ok := s.semStore.(knowledge.Remover); ok {
		return rem.Remove(ctx, capDocIDPrefix+capabilityID)
	}
	return nil
}
