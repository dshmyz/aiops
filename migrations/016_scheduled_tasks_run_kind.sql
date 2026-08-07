-- Phase 3 (E2 定时): 定时任务区分只读 vs 低风险 runbook 触发。
-- 新增 run_kind（read / runbook，默认 read，保持旧语义）+ runbook_slug（runbook 模板）。
-- 安全边界：定时只允许触发预先评审过的低风险 runbook 模板，绝不接受"定时执行任意 tool + input"。
ALTER TABLE copilot_scheduled_tasks
    ADD COLUMN run_kind VARCHAR(16) NOT NULL DEFAULT 'read' AFTER capability_name,
    ADD COLUMN runbook_slug VARCHAR(255) NULL DEFAULT NULL AFTER run_kind;
