package scheduler

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// 重新导出 store 包的报告类型，便于 scheduler 包的使用者无需直接引用 store。
type InspectionReport = store.InspectionReport
type InspectionTaskSummary = store.InspectionTaskSummary

// HTMLRenderer 把聚合后的报告数据渲染为 HTML 字符串。默认实现见
// DefaultHTMLRenderer；调用方可注入自定义渲染器（如邮件模板）。
type HTMLRenderer interface {
	Render(report InspectionReport) string
}

// Reporter 按时间窗口聚合定时任务的执行结果，生成结构化巡检报告。
// 它复用 ScheduledTaskStore 的 ListTasks + ListRuns，不引入新表。
type Reporter struct {
	store    store.ScheduledTaskStore
	renderer HTMLRenderer
	now      func() time.Time
}

// NewReporter 创建报告生成器。renderer 为 nil 时使用 DefaultHTMLRenderer。
func NewReporter(taskStore store.ScheduledTaskStore, renderer HTMLRenderer, now func() time.Time) *Reporter {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if renderer == nil {
		renderer = DefaultHTMLRenderer{}
	}
	return &Reporter{store: taskStore, renderer: renderer, now: now}
}

// GenerateForWindow 聚合 [windowStart, windowEnd) 内所有 task 的 runs，
// 生成一份巡检报告。无 runs 的 task 不计入报告。
func (r *Reporter) GenerateForWindow(ctx context.Context, period string, windowStart, windowEnd time.Time) (InspectionReport, error) {
	tasks, err := r.store.ListTasks(ctx, store.ScheduledTaskFilter{})
	if err != nil {
		return InspectionReport{}, fmt.Errorf("reporter: list tasks: %w", err)
	}

	summaries := make([]InspectionTaskSummary, 0, len(tasks))
	for _, task := range tasks {
		runs, err := r.store.ListRuns(ctx, task.ID, 0)
		if err != nil {
			return InspectionReport{}, fmt.Errorf("reporter: list runs for task %s: %w", task.ID, err)
		}
		summary := aggregateTaskRuns(task, runs, windowStart, windowEnd)
		if summary.TotalRuns == 0 {
			continue
		}
		summaries = append(summaries, summary)
	}

	// 按 TaskName 排序，保证报告顺序稳定。
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].TaskName == summaries[j].TaskName {
			return summaries[i].TaskID < summaries[j].TaskID
		}
		return summaries[i].TaskName < summaries[j].TaskName
	})

	succeeded, failed := 0, 0
	for _, s := range summaries {
		if s.FailedRuns > 0 {
			failed++
		} else {
			succeeded++
		}
	}

	report := InspectionReport{
		Period:         period,
		WindowStart:    windowStart,
		WindowEnd:      windowEnd,
		GeneratedAt:    r.now(),
		TotalTasks:     len(summaries),
		SucceededTasks: succeeded,
		FailedTasks:    failed,
		TaskSummaries:  summaries,
	}
	report.HTMLContent = r.renderer.Render(report)
	return report, nil
}

// GenerateDaily 生成前一天的日报。窗口为 [今天00:00 UTC - 24h, 今天00:00 UTC)。
func (r *Reporter) GenerateDaily(ctx context.Context) (InspectionReport, error) {
	now := r.now()
	windowEnd := truncateToDay(now)
	windowStart := windowEnd.Add(-24 * time.Hour)
	return r.GenerateForWindow(ctx, store.InspectionPeriodDaily, windowStart, windowEnd)
}

// aggregateTaskRuns 把单个 task 的 runs 聚合成摘要，只计入 [windowStart, windowEnd) 内的 runs。
func aggregateTaskRuns(task store.ScheduledTask, runs []store.ScheduledTaskRun, windowStart, windowEnd time.Time) InspectionTaskSummary {
	summary := InspectionTaskSummary{
		TaskID:         task.ID,
		TaskName:       task.Name,
		CapabilityName: task.CapabilityName,
	}
	for _, run := range runs {
		if run.StartedAt.Before(windowStart) || !run.StartedAt.Before(windowEnd) {
			continue
		}
		summary.TotalRuns++
		if run.Status == store.ScheduledTaskStatusSucceeded {
			summary.SucceededRuns++
		} else {
			summary.FailedRuns++
		}
		// runs 按 StartedAt 降序返回，第一个匹配的就是最新的
		if summary.LastStatus == "" {
			summary.LastStatus = run.Status
			summary.LastResultSummary = run.ResultSummary
			summary.LastError = run.Error
			summary.LastRunAt = run.StartedAt
		}
	}
	return summary
}

// truncateToDay 把时间截断到当天 00:00 UTC。
func truncateToDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
