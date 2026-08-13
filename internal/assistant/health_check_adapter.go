package assistant

import (
	"context"
	"database/sql"
)

// HealthCheckAdapter 适配 KnowledgeStore 为 httpapi.HealthCheckService 接口。
type HealthCheckAdapter struct {
	store *KnowledgeStore
}

func NewHealthCheckAdapter(store *KnowledgeStore) *HealthCheckAdapter {
	return &HealthCheckAdapter{store: store}
}

// GetRecentResults 返回最近的巡检结果。
func (a *HealthCheckAdapter) GetRecentResults(ctx context.Context, limit int) ([]map[string]any, error) {
	if a.store == nil {
		return nil, nil
	}
	rows, err := a.store.db.QueryContext(ctx,
		`SELECT alert_title, domain, tools_called, findings, created_at
		 FROM diagnosis_history
		 WHERE alert_title LIKE '巡检:%'
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]any
	for rows.Next() {
		var title, domain, toolsJSON, findings, createdAt string
		if err := rows.Scan(&title, &domain, &toolsJSON, &findings, &createdAt); err != nil {
			continue
		}
		results = append(results, map[string]any{
			"name":       title,
			"domain":     domain,
			"tools":      toolsJSON,
			"findings":   findings,
			"created_at": createdAt,
		})
	}
	return results, nil
}

// GetAnalysis 返回巡检统计分析。
func (a *HealthCheckAdapter) GetAnalysis(ctx context.Context) (map[string]any, error) {
	if a.store == nil {
		return nil, nil
	}
	var totalCount, healthyCount int
	err := a.store.db.QueryRowContext(ctx,
		`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN findings LIKE '%healthy%' OR findings LIKE '%ok%' THEN 1 ELSE 0 END), 0)
		 FROM diagnosis_history WHERE alert_title LIKE '巡检:%'`).Scan(&totalCount, &healthyCount)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"total_checks":   totalCount,
		"healthy_checks": healthyCount,
		"unhealthy":      totalCount - healthyCount,
	}, nil
}

// 确保接口实现
var _ interface {
	GetRecentResults(ctx context.Context, limit int) ([]map[string]any, error)
	GetAnalysis(ctx context.Context) (map[string]any, error)
} = (*HealthCheckAdapter)(nil)

// 确保数据库字段可访问
var _ = sql.ErrNoRows
