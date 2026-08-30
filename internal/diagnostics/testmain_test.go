package diagnostics_test

import (
	"os"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// TestMain 一次性注册测试域的动态读/写工具。测试域（kafka/minio/glusterfs）
// 不再由生产代码硬编码支持，测试通过动态注册在诊断服务解析时被识别，
// 与"已发布能力"的运行时行为保持一致。
func TestMain(m *testing.M) {
	defs := []tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{Name: "glusterfs.volume.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "glusterfs", ResourceType: "volume"},
			InputSchema: map[string]tools.DynamicInputField{

				"name": {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "minio.bucket.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
			InputSchema: map[string]tools.DynamicInputField{

				"name": {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "kafka.consumer_group.lag.read", Operation: tools.Read, Risk: tools.Low, Domain: "kafka", ResourceType: "consumer_group"},
			InputSchema: map[string]tools.DynamicInputField{

				"name": {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "topic.retention.set", Operation: tools.Write, Risk: tools.Medium, Domain: "kafka", ResourceType: "topic", RollbackDescription: "reset_to_previous"},
			InputSchema: map[string]tools.DynamicInputField{

				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true},
			},
		},
	}
	if err := tools.RegisterDynamicTools(defs); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
