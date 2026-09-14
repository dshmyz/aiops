package observability

import (
	"bytes"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// consoleTestConfig returns the EncoderConfig used by the console branch in
// InitLogger (colored level, HH:MM:SS time, short caller).
func consoleTestConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentConfig().EncoderConfig
	return cfg
}

// captureConsole logs the given fields with the keyValue console encoder and
// returns the single-line output.
func captureConsole(t *testing.T, fn func(*zap.Logger)) string {
	t.Helper()
	var sb bytes.Buffer
	enc := newKeyValueEncoder(consoleTestConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(&sb), zapcore.InfoLevel)
	logger := zap.New(core)
	fn(logger)
	return sb.String()
}

func TestKeyValueEncoderScalarFieldsFlat(t *testing.T) {
	out := captureConsole(t, func(l *zap.Logger) {
		l.Info("http_request",
			zap.String("method", "GET"),
			zap.String("path", "/v1/capabilities"),
			zap.Int("status", 200),
			zap.Int64("duration_ms", 3),
			zap.Bool("ok", true),
			zap.Float64("latency", 1.5),
		)
	})

	// 标量字段应以 key=value 形式扁平出现，且整行不应再包一层 JSON 对象。
	// 字符串带引号（消除空格/转义歧义），数字/布尔不加引号。
	for _, want := range []string{
		`method="GET"`,
		`path="/v1/capabilities"`,
		"status=200",
		"duration_ms=3",
		"ok=true",
		"latency=1.5",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing key=value %q", out, want)
		}
	}
	// console 模式下不应出现 JSON 字段对象尾巴（整行以 {...} 结尾）。
	if strings.HasSuffix(strings.TrimSpace(out), "}") {
		t.Errorf("console output still ends with a JSON object: %q", out)
	}
	// 消息文案 http_request 仍应可读。
	if !strings.Contains(out, "http_request") {
		t.Errorf("output %q missing message text", out)
	}
}

func TestKeyValueEncoderKeepsComplexFields(t *testing.T) {
	type inner struct{ Name string `json:"name"` }
	out := captureConsole(t, func(l *zap.Logger) {
		l.Info("msg",
			zap.Object("cap", zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
				enc.AddString("name", "minio.bucket.capacity.read")
				enc.AddInt("count", 2)
				return nil
			})),
			zap.Any("sample", map[string]any{"usage_pct": "88"}),
		)
	})

	// 复杂字段也不允许丢数据：值应出现在输出里（以 JSON 形式回退）。
	for _, want := range []string{
		"name",
		"minio.bucket.capacity.read",
		"usage_pct",
		"88",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q lost complex field value %q", out, want)
		}
	}
}

func TestKeyValueEncoderNoBraceOnlyObject(t *testing.T) {
	// 即便只有一个标量字段，也不应以 JSON 对象形式兜底输出。
	out := captureConsole(t, func(l *zap.Logger) {
		l.Info("m", zap.String("k", "v"))
	})
	if strings.HasSuffix(strings.TrimSpace(out), "}") {
		t.Errorf("single scalar field still rendered as JSON object: %q", out)
	}
}