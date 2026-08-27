package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func newSkillRouter(t *testing.T) (http.Handler, *store.MemorySkillStore) {
	t.Helper()
	skills := store.NewMemorySkillStore()
	readService := execution.NewReadOnlyService(&readRunner{}, nil)
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		readService,
		httpapi.WithSkills(skills),
	)
	return router, skills
}

func TestSkillsListRequiresNoWriteRole(t *testing.T) {
	t.Parallel()
	router, _ := newSkillRouter(t)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/skills", "", "viewer-1", []string{"viewer"}))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 for viewer list", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"skills":`) {
		t.Fatalf("body = %s, want skills array", res.Body.String())
	}
}

func TestSkillsCreateRequiresOperatorRole(t *testing.T) {
	t.Parallel()
	router, _ := newSkillRouter(t)
	body := `{"slug":"kafka-lag-sop","name":"Kafka 消费组排障","content":"先查 lag 再查 rebalance","risk_level":"low"}`
	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/skills", body, "viewer-1", []string{"viewer"}))
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403 for viewer create", res.Code, res.Body.String())
	}

	res2 := httptest.NewRecorder()
	router.ServeHTTP(res2, signedRequest(t, "/v1/skills", body, "operator-1", []string{"operator"}))
	if res2.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 for operator create", res2.Code, res2.Body.String())
	}
	if strings.Contains(res2.Body.String(), `"is_builtin":true`) {
		t.Fatalf("body = %s, custom skill must not be builtin", res2.Body.String())
	}
}

func TestSkillsUpdateAndDeleteByID(t *testing.T) {
	t.Parallel()
	router, skills := newSkillRouter(t)
	created, err := skills.CreateSkill(context.Background(), store.Skill{Slug: "minio-sop", Name: "MinIO SOP", Content: "检查 bucket"})
	if err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	// PUT 按 ID 更新（改名 + 停用）
	putBody := `{"slug":"minio-sop","name":"MinIO SOP v2","content":"检查 bucket 与 endpoint","risk_level":"low","is_enabled":false}`
	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequestWithMethod(t, http.MethodPut, "/v1/skills/"+created.ID, putBody, "operator-1", []string{"operator"}))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 for update", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"is_enabled":false`) {
		t.Fatalf("body = %s, want is_enabled=false after disable", res.Body.String())
	}

	// DELETE 非内置技能成功
	del := httptest.NewRecorder()
	router.ServeHTTP(del, signedRequestWithMethod(t, http.MethodDelete, "/v1/skills/"+created.ID, "", "admin-1", []string{"admin"}))
	if del.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200 for delete", del.Code, del.Body.String())
	}
	if !strings.Contains(del.Body.String(), `"deleted"`) {
		t.Fatalf("body = %s, want deleted ack", del.Body.String())
	}
}

func TestSkillsDeleteBuiltinRejected(t *testing.T) {
	t.Parallel()
	router, skills := newSkillRouter(t)
	builtin, err := skills.CreateSkill(context.Background(), store.Skill{Slug: "builtin-x", Name: "内置", Content: "c", IsBuiltin: true})
	if err != nil {
		t.Fatalf("seed builtin: %v", err)
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequestWithMethod(t, http.MethodDelete, "/v1/skills/"+builtin.ID, "", "admin-1", []string{"admin"}))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400 for builtin delete", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "内置技能不可删除") {
		t.Fatalf("body = %s, want builtin protection message", res.Body.String())
	}
}

func TestSkillsGetBySlugNotFound(t *testing.T) {
	t.Parallel()
	router, _ := newSkillRouter(t)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, signedRequest(t, "/v1/skills/nope", "", "viewer-1", []string{"viewer"}))
	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s, want 404", res.Code, res.Body.String())
	}
}