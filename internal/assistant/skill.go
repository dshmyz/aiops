package assistant

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// SkillSummary 是 AgentExecutor 需要的 Skill 精简视图。
// 通过接口而非直接依赖 store.Skill，保持 assistant 包的分层独立性。
type SkillSummary struct {
	Slug           string
	Name           string
	Description    string
	Content        string
	OutputContract string
	IsBuiltin      bool
}

// SkillLookup 是 AgentExecutor 查询 Skills 的依赖接口。
// store.SkillStore 实现此接口（ListSkillsByAction 返回 store.Skill，
// 通过 adapter 转换为 SkillSummary）。这样 assistant 包不直接依赖 store 包。
type SkillLookup interface {
	ListSkillsByAction(ctx context.Context, actionCode string) ([]SkillSummary, error)
}

// skillAlnumWord 匹配拉丁字母/数字组成的 token（中间件/服务名、版本号等：
// kafka、minio、glusterfs、v1.6...）。是相关性检索的主信号。
var skillAlnumWord = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_.-]*`)

// queryTerms 把用户消息切分成相关性检索词条：拉丁词 + 中文相邻二字组。
// 没有 Cjk 分词器时的轻量特征：`查看 kafka 消费组` → [kafka, 查看,查消,消费,费组]。
func queryTerms(msg string) []string {
	lower := strings.ToLower(msg)
	terms := skillAlnumWord.FindAllString(lower, -1)
	runes := []rune(lower)
	for i := 0; i+1 < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) {
			terms = append(terms, string(runes[i:i+2]))
		}
	}
	// 去重保持顺序；单字符 token（如 "a"）无区分度，过滤掉。
	seen := make(map[string]bool, len(terms))
	out := terms[:0]
	for _, t := range terms {
		if len([]rune(t)) < 2 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// RelevantSkills 按词条命中度选出与 query 最相关的 topN 技能。
// 命中 slug/name 权重 2，命中 description/content 权重 1；命中 0 个返回 nil。
// 只取 top-N 注入，避免全量正文塞爆 system prompt（agent 变笨的根因，见 agent_executor）。
func RelevantSkills(skills []SkillSummary, query string, topN int) []SkillSummary {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		s     SkillSummary
		score int
	}
	hits := make([]scored, 0, len(skills))
	for _, sk := range skills {
		if strings.TrimSpace(sk.Content) == "" && strings.TrimSpace(sk.OutputContract) == "" {
			continue // 无正文可注入的技能直接跳过，不占名额
		}
		identity := strings.ToLower(sk.Slug + "\x00" + sk.Name)
		haystack := strings.ToLower(sk.Description + "\x00" + sk.Content + "\x00" + sk.OutputContract)
		score := 0
		for _, t := range terms {
			if strings.Contains(identity, t) {
				score += 2
			} else if strings.Contains(haystack, t) {
				score += 1
			}
		}
		if score > 0 {
			hits = append(hits, scored{sk, score})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > topN {
		hits = hits[:topN]
	}
	out := make([]SkillSummary, len(hits))
	for i, h := range hits {
		out[i] = h.s
	}
	return out
}

// FormatSkillPrompt 把命中的技能正文渲染成注入 system prompt 的区块。
// 无技能或全部为空时返回空串。
func FormatSkillPrompt(skills []SkillSummary) string {
	var b strings.Builder
	for i, sk := range skills {
		if i == 0 {
			b.WriteString("\n\n## 适用的运维手册（SOP）\n")
		}
		label := sk.Name
		if label == "" {
			label = sk.Slug
		}
		b.WriteString(fmt.Sprintf("\n### %s\n", label))
		if sk.Content != "" {
			b.WriteString(strings.TrimSpace(sk.Content))
			b.WriteString("\n")
		}
		if sk.OutputContract != "" {
			b.WriteString(fmt.Sprintf("输出约束：%s\n", strings.TrimSpace(sk.OutputContract)))
		}
	}
	return b.String()
}
