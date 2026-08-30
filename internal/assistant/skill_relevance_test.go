package assistant

import (
	"strings"
	"testing"
)

func sampleSkills() []SkillSummary {
	return []SkillSummary{
		{Slug: "kafka-consumer-group", Name: "Kafka 消费组排障", Description: "kafka 消费组 lag 与重平衡排查", Content: "先查 consumer_lag，再查 rebalance...", OutputContract: "返回 lag 与分区归属"},
		{Slug: "minio-http", Name: "MinIO 访问排障", Description: "minio 桶访问与权限", Content: "检查 bucket policy 与 endpoint...", OutputContract: "返回策略与访问结论"},
		{Slug: "storage-vol", Name: "存储卷状态", Description: "glusterfs/存储卷健康", Content: "检查卷状态与副本数...", OutputContract: "返回卷健康结论"},
	}
}

func TestRelevantSkillsRanksByTopic(t *testing.T) {
	skills := sampleSkills()
	// kafka 主题的查询——应命中 kafka，且其命中度高于其它两个。
	rel := RelevantSkills(skills, "查看 kafka 消费组状态", 3)
	if len(rel) == 0 {
		t.Fatalf("expected at least one skill matched, got nil")
	}
	if rel[0].Slug != "kafka-consumer-group" {
		t.Fatalf("expected kafka-consumer-group to rank first, got %s", rel[0].Slug)
	}
}

func TestRelevantSkillsLimitsToTopN(t *testing.T) {
	rel := RelevantSkills(sampleSkills(), "查看 kafka 消费组状态", 1)
	if len(rel) != 1 || rel[0].Slug != "kafka-consumer-group" {
		t.Fatalf("topN=1 should return only the best match, got %d", len(rel))
	}
}

func TestRelevantSkillsNoMatch(t *testing.T) {
	// 无任何命中词条时返回 nil，而不是返回不相关的技能正文。
	if rel := RelevantSkills(sampleSkills(), "？。！", 3); len(rel) != 0 {
		t.Fatalf("expected no match for opaque query, got %d skills", len(rel))
	}
}

func TestRelevantSkillsSkipsEmptyBody(t *testing.T) {
	skills := append(sampleSkills(), SkillSummary{Slug: "empty", Description: "kafka 空档", Content: "", OutputContract: ""})
	rel := RelevantSkills(skills, "kafka", 3)
	for _, s := range rel {
		if s.Slug == "empty" {
			t.Fatalf("skill with empty content/output must not be selected")
		}
	}
}

func TestFormatSkillPrompt(t *testing.T) {
	out := FormatSkillPrompt(RelevantSkills(sampleSkills(), "查看 kafka 消费组状态", 2))
	if !strings.Contains(out, "## 适用的运维手册（SOP）") {
		t.Fatalf("missing SOP header: %q", out)
	}
	if !strings.Contains(out, "Kafka 消费组排障") {
		t.Fatalf("missing skill name in prompt: %q", out)
	}
	if !strings.Contains(out, "输出约束：返回 lag 与分区归属") {
		t.Fatalf("missing output contract in prompt: %q", out)
	}
	// 只注入命中的 top-N，不把全部技能正文灌进去
	if strings.Contains(out, "MinIO 访问排障") {
		t.Fatalf("non-matching skill content leaked into prompt: %q", out)
	}
}
