package diagnostics

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RunbookTemplate 描述一个诊断流程模板：主题说明与域无关的通用检查维度清单。
// 新增诊断模板的单一扩展点：向注册表 registerRunbook 注册一项即可，
// 校验（validRunbook）与维度生成（genericCheckpoints）都会自动识别，无需再改散落的枚举。
type RunbookTemplate struct {
	Description string
	Checkpoints []string
}

// runbookTemplates 内建诊断模板注册表。
// 键为 runbook 名；空 runbook 在消费端统一兜底为 health。
// 内建项可由 runbooks.yaml 增量注册覆盖（RegisterRunbooksFromYAML）。
var runbookTemplates = map[string]RunbookTemplate{
	"health": {
		Description: "通用健康巡检：可达性、核心资源、错误率与变更",
		Checkpoints: []string{
			"服务可达性与进程状态",
			"核心资源指标（CPU/内存/磁盘/网络）",
			"错误率与慢请求占比",
			"近期变更与关联事件（发布/配置/扩容）",
		},
	},
	"capacity": {
		Description: "容量评估：用量、趋势、扩容阈值与闲置资源",
		Checkpoints: []string{
			"当前用量与已用容量（按资源类型统计）",
			"使用趋势（近 30 天，判断持续增长或偶发波动）",
			"扩容阈值与触发条件（预留量是否足够）",
			"闲置与浪费资源识别",
		},
	},
	"consumer_lag": {
		Description: "消费/复制滞后巡检：积压量、速率对比与趋势",
		Checkpoints: []string{
			"消费者组/副本滞后量（积压消息数）",
			"消费速率 vs 生产速率（是否持续追不上）",
			"滞后趋势（随时间收敛还是发散）",
			"消费者实例健康与并发度",
		},
	},
}

// registerRunbook 注册或覆盖一个诊断模板。测试与外部扩展通过它新增模板，
// 不必改动 validRunbook/genericCheckpoints 的实现。
func registerRunbook(name string, t RunbookTemplate) {
	runbookTemplates[name] = t
}

// lookupRunbook 按名查找诊断模板。
func lookupRunbook(name string) (RunbookTemplate, bool) {
	t, ok := runbookTemplates[name]
	return t, ok
}

// runbooksConfig runbooks.yaml 的顶层结构（复用 models.yaml 的配置模式）。
type runbooksConfig struct {
	Runbooks map[string]RunbookTemplate `yaml:"runbooks"`
}

// RegisterRunbooksFromYAML 从 runbooks.yaml 增量注册诊断模板：文件里声明的
// runbook 同名覆盖内建项、新名追加，未提到的内建项保持不变，实现"不改代码
// 加模板"。返回本次注册的 runbook 名（按字母序）。显式指定的文件读取或
// 解析失败、或某条目缺 checkpoints 时返回错误——与 LoadModelRegistry 的
// 显式失败原则一致，避免操作者以为配置已生效而实际没有。
func RegisterRunbooksFromYAML(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runbooks config: %w", err)
	}
	var cfg runbooksConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse runbooks config: %w", err)
	}
	names := make([]string, 0, len(cfg.Runbooks))
	for name, template := range cfg.Runbooks {
		if err := validateYAMLRunbook(name, template); err != nil {
			return nil, err
		}
		registerRunbook(name, template)
		names = append(names, name)
	}
	sortStrings(names)
	return names, nil
}

func validateYAMLRunbook(name string, template RunbookTemplate) error {
	if name == "" {
		return fmt.Errorf("runbooks config: runbook 名不能为空")
	}
	if len(template.Checkpoints) == 0 {
		return fmt.Errorf("runbooks config: runbook %q 缺少 checkpoints", name)
	}
	return nil
}

func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
