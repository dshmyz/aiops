package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)
		`)
	}
	if err != nil {
		return err
	}
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

// SearchFeedback 搜索相似问题的用户反馈
func (s *KnowledgeStore) SearchFeedback(ctx context.Context, query string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT query, rating, correction, tools_called, created_at
		 FROM feedback_learning
		 WHERE query LIKE ? OR correction LIKE ?
		 ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		var q, correction, toolsJSON, createdAt string
		var rating int
		if err := rows.Scan(&q, &rating, &correction, &toolsJSON, &createdAt); err != nil {
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
	toolsJSON, _ := json.Marshal(toolsCalled)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO diagnosis_history (alert_title, domain, tools_called, findings, recommendations) VALUES (?, ?, ?, ?, ?)`,
		title, domain, string(toolsJSON), findings, recommendations)
	return err
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
		// 从工具名推断 domain
		toolName := toolCalls[0].Tool
		for _, d := range []string{"kafka", "minio", "glusterfs", "moonlightbox"} {
			if len(toolName) > len(d) && toolName[:len(d)] == d {
				domain = d
				break
			}
		}
	}
	_ = s.Save(ctx, alertTitle, domain, toolsCalled, fmt.Sprint(findings), "")
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
