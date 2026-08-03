-- 结果准 #5: 执行后验证 + dry-run 预览持久化。
-- tool_executions 增加 verification JSON 列，执行成功后的验证结果落库，
-- 支持事后复盘（/v1/executions 返回）。
ALTER TABLE tool_executions ADD COLUMN verification JSON NULL;

-- action_plans 增加 dry_run JSON 列，写计划创建时的 dry-run 预览结果
-- （DryRunResult + SuggestedStrategy）落库，供复盘确认时的完整执行计划。
ALTER TABLE action_plans ADD COLUMN dry_run JSON NULL;
