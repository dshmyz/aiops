package assistant

import (
	"context"
	"log"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fallbackChat 在主模型失败时自动切换到备用模型。
type fallbackChat struct {
	primary   model.BaseChatModel
	fallback  model.BaseChatModel
	threshold int // 连续失败多少次后切换
	failures  int
	swapped   bool
}

// newFallbackChat 创建带降级的 chat model。
// fallback 为 nil 时退化为纯 retry 行为。
func newFallbackChat(primary, fallback model.BaseChatModel, threshold int) model.BaseChatModel {
	if fallback == nil || threshold <= 0 {
		return primary
	}
	return &fallbackChat{primary: primary, fallback: fallback, threshold: threshold}
}

func (f *fallbackChat) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	chat := f.primary
	if f.swapped {
		chat = f.fallback
	}

	resp, err := chat.Generate(ctx, input, opts...)
	if err == nil {
		f.failures = 0 // 成功则重置计数
		return resp, nil
	}

	// 判断是否需要降级
	if isRateLimitError(err) {
		f.failures++
		log.Printf("[fallback] rate limit hit, consecutive_failures=%d (threshold=%d)", f.failures, f.threshold)
		if f.failures >= f.threshold && !f.swapped {
			log.Printf("[fallback] switching to fallback model after %d consecutive rate limits", f.failures)
			f.swapped = true
			// 用备用模型重试一次
			resp, err = f.fallback.Generate(ctx, input, opts...)
			if err == nil {
				return resp, nil
			}
		}
	} else {
		f.failures = 0
	}
	return resp, err
}

func (f *fallbackChat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	chat := f.primary
	if f.swapped {
		chat = f.fallback
	}
	return chat.Stream(ctx, input, opts...)
}
