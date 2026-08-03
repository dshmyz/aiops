package scheduler

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// DefaultHTMLRenderer 把巡检报告渲染为自包含的 HTML 文档。输出包含：
// 标题、时间窗口、概览统计、按任务分组的详情表格。样式内联，适合邮件发送。
type DefaultHTMLRenderer struct{}

func (DefaultHTMLRenderer) Render(report InspectionReport) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>巡检报告</title>
<style>
  body { font-family: -apple-system, "Helvetica Neue", sans-serif; margin: 0; padding: 20px; color: #1d1d1f; background: #f5f5f7; }
  .container { max-width: 800px; margin: 0 auto; background: #fff; border-radius: 18px; padding: 32px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); }
  h1 { margin: 0 0 8px; font-size: 24px; }
  .window { color: #6e6e73; font-size: 14px; margin-bottom: 24px; }
  .overview { display: flex; gap: 16px; margin-bottom: 28px; }
  .stat { flex: 1; background: #f5f5f7; border-radius: 12px; padding: 16px; text-align: center; }
  .stat .num { font-size: 28px; font-weight: 600; }
  .stat .label { font-size: 13px; color: #6e6e73; margin-top: 4px; }
  .stat.failed .num { color: #ff3b30; }
  .stat.ok .num { color: #34c759; }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th { text-align: left; padding: 10px 12px; border-bottom: 2px solid #e5e5ea; color: #6e6e73; font-weight: 600; }
  td { padding: 10px 12px; border-bottom: 1px solid #f0f0f2; }
  .status-badge { display: inline-block; padding: 2px 10px; border-radius: 10px; font-size: 12px; font-weight: 500; }
  .status-succeeded { background: #e8f8ed; color: #1b8a3f; }
  .status-failed { background: #ffeaea; color: #c53030; }
  .error { color: #c53030; font-size: 13px; }
</style>
</head>
<body>
<div class="container">
`)
	sb.WriteString(fmt.Sprintf("<h1>巡检报告 — %s</h1>\n", html.EscapeString(report.Period)))
	sb.WriteString(fmt.Sprintf(`<div class="window">%s ~ %s（生成于 %s）</div>`+"\n",
		html.EscapeString(report.WindowStart.In(time.UTC).Format("2006-01-02 15:04 MST")),
		html.EscapeString(report.WindowEnd.In(time.UTC).Format("2006-01-02 15:04 MST")),
		html.EscapeString(report.GeneratedAt.In(time.UTC).Format("2006-01-02 15:04 MST")),
	))

	// 概览统计
	failureRate := 0.0
	if report.TotalTasks > 0 {
		failureRate = float64(report.FailedTasks) / float64(report.TotalTasks) * 100
	}
	sb.WriteString(`<div class="overview">`)
	sb.WriteString(fmt.Sprintf(`<div class="stat"><div class="num">%d</div><div class="label">任务总数</div></div>`, report.TotalTasks))
	sb.WriteString(fmt.Sprintf(`<div class="stat ok"><div class="num">%d</div><div class="label">正常</div></div>`, report.SucceededTasks))
	sb.WriteString(fmt.Sprintf(`<div class="stat failed"><div class="num">%d</div><div class="label">异常</div></div>`, report.FailedTasks))
	sb.WriteString(fmt.Sprintf(`<div class="stat failed"><div class="num">%.0f%%</div><div class="label">异常率</div></div>`, failureRate))
	sb.WriteString(`</div>`+"\n")

	// 任务详情表格
	sb.WriteString(`<table>
<thead><tr><th>任务名称</th><th>Capability</th><th>执行次数</th><th>成功/失败</th><th>最近状态</th><th>最近错误</th></tr></thead>
<tbody>
`)
	for _, s := range report.TaskSummaries {
		statusClass := "status-succeeded"
		if s.LastStatus != "succeeded" {
			statusClass = "status-failed"
		}
		sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%d</td><td>%d / %d</td><td><span class=\"status-badge %s\">%s</span></td><td class=\"error\">%s</td></tr>\n",
			html.EscapeString(s.TaskName),
			html.EscapeString(s.CapabilityName),
			s.TotalRuns,
			s.SucceededRuns,
			s.FailedRuns,
			statusClass,
			html.EscapeString(s.LastStatus),
			html.EscapeString(s.LastError),
		))
	}
	sb.WriteString(`</tbody>
</table>
`)
	sb.WriteString(`</div>
</body>
</html>`)
	return sb.String()
}
