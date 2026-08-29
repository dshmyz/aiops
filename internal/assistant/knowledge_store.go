package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"unicode"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// KnowledgeStore 存储和检索历史诊断经验
type KnowledgeStore struct {
	db *sql.DB
}

func NewKnowledgeStore(db *sql.DB) *KnowledgeStore {
	return &KnowledgeStore{db: db}
}

// Init 创建表结构（先试 MySQL 方言，失败回退 SQLite 方言）。
// 迁移系统（migrations/020）也会建表，这里作为兜底。
func (s *KnowledgeStore) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS diagnosis_history (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			alert_title TEXT,
			domain VARCHAR(64) DEFAULT '',
			tools_called TEXT,
			findings TEXT,
			recommendations TEXT,
			reasoning TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		// SQLite 方言回退
		_, err = s.db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS diagnosis_history (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				alert_title TEXT,
				domain TEXT DEFAULT '',
				tools_called TEXT,
				findings TEXT,
				recommendations TEXT,
				reasoning TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)
		`)
	}
	if err != nil {
		return err
	}
	// 兼容旧表：补列（表已存在时 ALTER 成功，新表时列已存在报错，两者都忽略）
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE diagnosis_history ADD COLUMN reasoning TEXT`)
	// 反馈学习表：记录用户的 👍/👎 和纠错，用于下次类似问题的提示
	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS feedback_learning (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			query TEXT,
			rating INTEGER,
			correction TEXT,
			tools_called TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		_, err = s.db.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS feedback_learning (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				query TEXT,
				rating INTEGER,
				correction TEXT,
				tools_called TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)
		`)
	}
	return err
}

// SaveFeedback 保存用户反馈并关联诊断上下文
func (s *KnowledgeStore) SaveFeedback(ctx context.Context, query string, rating int, correction string, toolsCalled []string) error {
	toolsJSON, _ := json.Marshal(toolsCalled)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback_learning (query, rating, correction, tools_called) VALUES (?, ?, ?, ?)`,
		query, rating, correction, string(toolsJSON))
	return err
}

// SearchFeedback 搜索相似问题的用户反馈。query 是完整的用户消息原文，直接
// LIKE 全文几乎不可能命中（历史 query 也是整句），所以这里把消息切成关键词，
// 多个 LIKE 条件取 OR，按命中词数降序——命中越多越相关。至少命中 2 个词
// 才返回，避免单凭"的/日志"这类泛词误召回。
func (s *KnowledgeStore) SearchFeedback(ctx context.Context, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 5
	}
	keywords := feedbackKeywords(query)
	if len(keywords) == 0 {
		return nil, nil
	}
	var conds []string
	var scoreParts []string
	var args []any
	for _, kw := range keywords {
		conds = append(conds, `(query LIKE ? OR correction LIKE ?)`)
		scoreParts = append(scoreParts, `CASE WHEN query LIKE ? OR correction LIKE ? THEN 1 ELSE 0 END`)
		// 每个关键词占 4 个参数：WHERE 2 个 + score 2 个
		args = append(args, "%"+kw+"%", "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
	}
	args = append(args, limit)

	sql := `SELECT query, rating, correction, tools_called, created_at,
			(` + strings.Join(scoreParts, " + ") + `) AS hits
		 FROM feedback_learning
		 WHERE (` + strings.Join(conds, " OR ") + `)
		 ORDER BY hits DESC, created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		var q, correction, toolsJSON, createdAt string
		var rating, hits int
		if err := rows.Scan(&q, &rating, &correction, &toolsJSON, &createdAt, &hits); err != nil {
			continue
		}
		if hits < 2 && len(keywords) >= 2 {
			continue
		}
		results = append(results, map[string]any{
			"query":      q,
			"rating":     rating,
			"correction": correction,
			"tools":      toolsJSON,
			"created_at": createdAt,
		})
	}
	return results, nil
}

// feedbackKeywords 从用户消息中提取检索关键词：去标点、按空白切分、去掉
// 过短的词（<2 字节）和常见停用词。与分词无关的朴素策略——够用于召回，
// 不追求精度。
func feedbackKeywords(query string) []string {
	stop := map[string]bool{
		"的": true, "了": true, "吗": true, "呢": true, "是": true, "在": true,
		"和": true, "与": true, "怎么": true, "什么": true, "为什么": true,
		"查一下": true, "帮忙": true, "请问": true, "看看": true, "排查": true,
		"the": true, "a": true, "an": true, "is": true, "of": true, "to": true,
		"what": true, "why": true, "how": true,
	}
	split := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var kws []string
	for _, w := range split {
		w = strings.ToLower(strings.TrimSpace(w))
		if len(w) < 2 || stop[w] || seen[w] {
			continue
		}
		seen[w] = true
		kws = append(kws, w)
	}
	// 中文整句无空格切不出词：整句作为单一关键词回退，仍优于 LIKE 全文
	if len(kws) == 0 && strings.TrimSpace(query) != "" {
		kws = []string{strings.TrimSpace(query)}
	}
	return kws
}

// AnalyzeFeedback 分析累积的反馈模式，返回改进建议。
func (s *KnowledgeStore) AnalyzeFeedback(ctx context.Context) (map[string]any, error) {
	// 统计好评/差评数量
	var posCount, negCount int
	err := s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN rating > 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN rating < 0 THEN 1 ELSE 0 END), 0)
		 FROM feedback_learning`).Scan(&posCount, &negCount)
	if err != nil {
		return nil, err
	}

	// 找出最常见的纠错关键词
	rows, err := s.db.QueryContext(ctx,
		`SELECT correction FROM feedback_learning WHERE rating < 0 AND correction != '' LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	corrections := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err == nil && c != "" {
			corrections = append(corrections, c)
		}
	}

	return map[string]any{
		"positive_count": posCount,
		"negative_count": negCount,
		"total":          posCount + negCount,
		"approval_rate":  float64(posCount) / float64(max(posCount+negCount, 1)),
		"recent_corrections": corrections,
	}, nil
}

// Save 保存一次诊断记录
func (s *KnowledgeStore) Save(ctx context.Context, title, domain string, toolsCalled []string, findings, recommendations string) error {
	return s.SaveWithReasoning(ctx, title, domain, toolsCalled, findings, recommendations, "")
}

// SaveWithReasoning 保存诊断记录并附带 LLM 决策链。
func (s *KnowledgeStore) SaveWithReasoning(ctx context.Context, title, domain string, toolsCalled []string, findings, recommendations, reasoning string) error {
	toolsJSON, _ := json.Marshal(toolsCalled)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO diagnosis_history (alert_title, domain, tools_called, findings, recommendations, reasoning) VALUES (?, ?, ?, ?, ?, ?)`,
		title, domain, string(toolsJSON), findings, recommendations, reasoning)
	return err
}

// RecentProbes 返回最近的巡检记录（由 HealthChecker 写入，alert_title 形如
// "巡检: <name>"），供系统态势聚合（system.posture.read）消费。按写入时间
// 倒序，limit<=0 时取 10 条。
func (s *KnowledgeStore) RecentProbes(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT alert_title, domain, findings, created_at
		 FROM diagnosis_history
		 WHERE alert_title LIKE '巡检:%'
		 ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		var title, domain, findings, createdAt string
		if err := rows.Scan(&title, &domain, &findings, &createdAt); err != nil {
			continue
		}
		results = append(results, map[string]any{
			"title":      title,
			"domain":     domain,
			"findings":   findings,
			"created_at": createdAt,
		})
	}
	return results, nil
}

// Search 搜索相似诊断历史
func (s *KnowledgeStore) Search(ctx context.Context, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT alert_title, domain, tools_called, findings, recommendations, created_at
		 FROM diagnosis_history
		 WHERE alert_title LIKE ? OR findings LIKE ?
		 ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		var title, domain, toolsJSON, findings, recs, createdAt string
		if err := rows.Scan(&title, &domain, &toolsJSON, &findings, &recs, &createdAt); err != nil {
			continue
		}
		results = append(results, map[string]any{
			"title":       title,
			"domain":      domain,
			"tools":       toolsJSON,
			"findings":    findings,
			"recommendations": recs,
			"created_at":  createdAt,
		})
	}
	return results, nil
}

// SaveFromToolCalls 从工具调用结果中提取诊断记录
func (s *KnowledgeStore) SaveFromToolCalls(ctx context.Context, alertTitle string, toolCalls []ToolCallLog) {
	if len(toolCalls) == 0 {
		return
	}
	var toolsCalled []string
	var findings []string
	for _, tc := range toolCalls {
		toolsCalled = append(toolsCalled, tc.Tool)
		if tc.Error != "" {
			findings = append(findings, fmt.Sprintf("%s failed: %s", tc.Tool, tc.Error))
		} else if tc.Output != nil {
			if summary, ok := tc.Output["summary"].(string); ok && summary != "" {
				findings = append(findings, summary)
			}
		}
	}
	domain := ""
	if len(toolCalls) > 0 {
		// 从注册表取工具域，避免硬编码域名前缀。
		if tool, ok := tools.Lookup(toolCalls[0].Tool); ok {
			domain = tool.Domain
		}
	}
	if err := s.Save(ctx, alertTitle, domain, toolsCalled, fmt.Sprint(findings), ""); err != nil {
		log.Printf("[knowledge] save diagnostic record failed: %v", err)
	}
}

// SaveFromToolCallsWithReasoning 保存工具调用记录并附带 LLM 决策链。
func (s *KnowledgeStore) SaveFromToolCallsWithReasoning(ctx context.Context, alertTitle string, toolCalls []ToolCallLog, reasoning string) {
	if len(toolCalls) == 0 {
		return
	}
	var toolsCalled []string
	var findings []string
	for _, tc := range toolCalls {
		toolsCalled = append(toolsCalled, tc.Tool)
		if tc.Error != "" {
			findings = append(findings, fmt.Sprintf("%s failed: %s", tc.Tool, tc.Error))
		} else if tc.Output != nil {
			if summary, ok := tc.Output["summary"].(string); ok && summary != "" {
				findings = append(findings, summary)
			}
		}
	}
	domain := ""
	if len(toolCalls) > 0 {
		if tool, ok := tools.Lookup(toolCalls[0].Tool); ok {
			domain = tool.Domain
		}
	}
	if err := s.SaveWithReasoning(ctx, alertTitle, domain, toolsCalled, fmt.Sprint(findings), "", reasoning); err != nil {
		log.Printf("[knowledge] save diagnostic record (with reasoning) failed: %v", err)
	}
}

// ConversationSummary 结构化对话摘要
type ConversationSummary struct {
	Intent   string   `json:"intent"`   // 用户意图
	Tools    []string `json:"tools"`    // 调用的工具
	Outcome  string   `json:"outcome"`  // 结果（success/error）
	KeyFacts []string `json:"key_facts"` // 关键发现
}

// SaveConversationSummary 保存结构化对话摘要
func (s *KnowledgeStore) SaveConversationSummary(ctx context.Context, query string, summary ConversationSummary) error {
	summaryJSON, _ := json.Marshal(summary)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO diagnosis_history (alert_title, domain, tools_called, findings, recommendations) VALUES (?, ?, ?, ?, ?)`,
		query, summary.Intent, string(summaryJSON), summary.Outcome, fmt.Sprint(summary.KeyFacts))
	return err
}
