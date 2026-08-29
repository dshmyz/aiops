package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWebhookNotifierDeliversSignedPayload 验证出站 webhook 的信封结构、
// 类型头与 HMAC 签名（与告警入站 webhook 同一验签模型）。
func TestWebhookNotifierDeliversSignedPayload(t *testing.T) {
	const secret = "test-webhook-secret"
	var gotBody []byte
	var gotSig, gotType, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Signature")
		gotType = r.Header.Get("X-Notification-Type")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	notifier := NewWebhookNotifier(srv.URL, secret)
	err := notifier.NotifyConfirmation(context.Background(), ConfirmationRequest{
		PlanID:            "plan-1",
		ConfirmationToken: "tok-1",
		ToolName:          "topic.retention.set",
		Risk:              "medium",
		Subject:           "alert-llm-diag",
		Input:             map[string]any{"topic": "orders"},
	})
	if err != nil {
		t.Fatalf("NotifyConfirmation: %v", err)
	}

	// 签名可被接收方用同一密钥复算验证（与 verifyAlertWebhookSignature 同构）。
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	if !hmac.Equal([]byte(gotSig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		t.Fatalf("X-Signature mismatch: got %q", gotSig)
	}
	if gotType != "confirmation_required" {
		t.Fatalf("X-Notification-Type = %q", gotType)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	// 信封字段 + 嵌入展平的确认字段。
	for _, want := range []string{`"type":"confirmation_required"`, `"plan_id":"plan-1"`, `"confirmation_token":"tok-1"`, `"tool_name":"topic.retention.set"`, `"sent_at":`} {
		if !contains(string(gotBody), want) {
			t.Fatalf("payload missing %s: %s", want, gotBody)
		}
	}
}

// TestWebhookNotifierNoSecretNoHeader 验证未配置密钥时不签名、投递照常。
func TestWebhookNotifierNoSecretNoHeader(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	notifier := NewWebhookNotifier(srv.URL, "")
	if err := notifier.NotifyConfirmation(context.Background(), ConfirmationRequest{PlanID: "p"}); err != nil {
		t.Fatalf("NotifyConfirmation: %v", err)
	}
	if gotSig != "" {
		t.Fatalf("X-Signature should be absent without secret, got %q", gotSig)
	}
}

// TestWebhookNotifierNon2xxIsError 验证接收方非 2xx 返回错误（由
// MultiNotifier 记录并继续扇出）。
func TestWebhookNotifierNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	notifier := NewWebhookNotifier(srv.URL, "")
	if err := notifier.NotifyConfirmation(context.Background(), ConfirmationRequest{PlanID: "p"}); err == nil {
		t.Fatal("HTTP 500 must surface as error")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
