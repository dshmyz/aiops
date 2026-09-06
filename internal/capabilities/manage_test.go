package capabilities_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestManagerListIncludesDiscoveredAndPublishedWithValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml"), validReadYAML("needs_review"))
	mustWrite(t, filepath.Join(root, "published", "kafka.consumer.offset.read.yaml"), strings.Replace(validReadYAML("published"), "name: minio.bucket.capacity.read", "name: kafka.consumer.offset.read", 1))

	manager := capabilities.NewManager(root, nil)
	items, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("List returned %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two managed capabilities", items)
	}
	if items[0].Name != "minio.bucket.capacity.read" || items[0].Source != "discovered" || !items[0].Validation.Valid {
		t.Fatalf("first item = %+v, want valid discovered capability", items[0])
	}
	if items[1].Name != "kafka.consumer.offset.read" || items[1].Source != "published" || !items[1].Validation.Valid {
		t.Fatalf("second item = %+v, want valid published capability", items[1])
	}
}

func TestManagerSaveDraftWritesDiscoveredYAMLAndRejectsTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager := capabilities.NewManager(root, nil)

	capability := managedReadCapability("minio.bucket.capacity.read", "needs_review")
	saved, err := manager.SaveDraft(context.Background(), capability)
	if err != nil {
		t.Fatalf("SaveDraft returned %v", err)
	}
	if saved.Source != "discovered" || saved.Status != "needs_review" {
		t.Fatalf("saved = %+v, want discovered draft", saved)
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml")); err != nil {
		t.Fatalf("draft file was not written: %v", err)
	}

	capability.Name = "../escape"
	if _, err := manager.SaveDraft(context.Background(), capability); !errors.Is(err, capabilities.ErrInvalidCapabilityName) {
		t.Fatalf("SaveDraft traversal error = %v, want ErrInvalidCapabilityName", err)
	}
}

func TestManagerPublishMovesReadDraftAndRejectsUnsafeCapabilities(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml"), validReadYAML("needs_review"))
	mustWrite(t, filepath.Join(root, "discovered", tools.ClusterStatusRead+".yaml"), strings.Replace(validReadYAML("needs_review"), "name: minio.bucket.capacity.read", "name: "+tools.ClusterStatusRead, 1))
	manager := capabilities.NewManager(root, nil)

	published, err := manager.Publish(context.Background(), "minio.bucket.capacity.read")
	if err != nil {
		t.Fatalf("Publish returned %v", err)
	}
	if published.Source != "published" || published.Status != "published" {
		t.Fatalf("published = %+v, want published item", published)
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml")); !os.IsNotExist(err) {
		t.Fatalf("discovered draft still exists, stat = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "published", "minio.bucket.capacity.read.yaml")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
	if _, err := manager.Publish(context.Background(), tools.ClusterStatusRead); !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("conflict publish error = %v, want ErrCapabilityNameConflict", err)
	}
}

func TestManagerPublishConflictWithStaticToolCarriesNameAndSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "discovered", fmt.Sprintf("%s.yaml", tools.ClusterStatusRead)),
		strings.Replace(validReadYAML("needs_review"), "name: minio.bucket.capacity.read", "name: "+tools.ClusterStatusRead, 1))
	manager := capabilities.NewManager(root, nil)

	_, err := manager.Publish(context.Background(), tools.ClusterStatusRead)
	if err == nil {
		t.Fatalf("Publish succeeded, want conflict error")
	}
	if !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("error = %v, want ErrCapabilityNameConflict", err)
	}
	if !strings.Contains(err.Error(), tools.ClusterStatusRead) {
		t.Fatalf("error message %q does not contain capability name %q", err.Error(), tools.ClusterStatusRead)
	}
	if !strings.Contains(err.Error(), "tool") {
		t.Fatalf("error message %q does not indicate conflict source is a tool", err.Error())
	}
}

func TestManagerPublishConflictWithPublishedFileCarriesNameAndSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "redis.cluster.info.read.yaml"),
		strings.Replace(validReadYAML("published"), "name: minio.bucket.capacity.read", "name: redis.cluster.info.read", 1))
	manager := capabilities.NewManager(root, nil)

	// discovered 同名草稿存在，尝试发布会因 published 文件已存在而冲突
	mustWrite(t, filepath.Join(root, "discovered", "redis.cluster.info.read.yaml"),
		strings.Replace(validReadYAML("needs_review"), "name: minio.bucket.capacity.read", "name: redis.cluster.info.read", 1))

	_, err := manager.Publish(context.Background(), "redis.cluster.info.read")
	if err == nil {
		t.Fatalf("Publish succeeded, want conflict error")
	}
	if !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("error = %v, want ErrCapabilityNameConflict", err)
	}
	if !strings.Contains(err.Error(), "redis.cluster.info.read") {
		t.Fatalf("error message %q does not contain capability name", err.Error())
	}
	if !strings.Contains(err.Error(), "published") {
		t.Fatalf("error message %q does not indicate conflict source is a published capability", err.Error())
	}
}

func TestManagerPublishMovesValidWriteDraftToPublished(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "discovered", "minio.bucket.capacity.set.yaml"), validWriteYAMLWithStatus("needs_review"))
	manager := capabilities.NewManager(root, nil)

	published, err := manager.Publish(context.Background(), "minio.bucket.capacity.set")
	if err != nil {
		t.Fatalf("Publish returned %v", err)
	}
	if published.Source != "published" || published.Status != "published" || published.Operation != tools.Write {
		t.Fatalf("published = %+v, want published write capability", published)
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "minio.bucket.capacity.set.yaml")); !os.IsNotExist(err) {
		t.Fatalf("discovered draft still exists, stat = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "published", "minio.bucket.capacity.set.yaml")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
}

func TestManagerUnpublishMovesPublishedCapabilityBackToReview(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), validReadYAML("published"))
	manager := capabilities.NewManager(root, nil)

	unpublished, err := manager.Unpublish(context.Background(), "minio.bucket.capacity.read")
	if err != nil {
		t.Fatalf("Unpublish returned %v", err)
	}
	if unpublished.Source != "discovered" || unpublished.Status != "needs_review" {
		t.Fatalf("unpublished = %+v, want discovered needs_review", unpublished)
	}
	if _, err := os.Stat(filepath.Join(root, "published", "minio.bucket.capacity.read.yaml")); !os.IsNotExist(err) {
		t.Fatalf("published file still exists, stat = %v", err)
	}
}

func TestManagerUnpublishConflictWithDiscoveredDraftCarriesNameAndSource(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	// published 和 discovered 同名，下线时会被 discovered 草稿卡住
	mustWrite(t, filepath.Join(root, "published", "redis.cluster.info.read.yaml"),
		strings.Replace(validReadYAML("published"), "name: minio.bucket.capacity.read", "name: redis.cluster.info.read", 1))
	mustWrite(t, filepath.Join(root, "discovered", "redis.cluster.info.read.yaml"),
		strings.Replace(validReadYAML("needs_review"), "name: minio.bucket.capacity.read", "name: redis.cluster.info.read", 1))
	manager := capabilities.NewManager(root, nil)

	_, err := manager.Unpublish(context.Background(), "redis.cluster.info.read")
	if err == nil {
		t.Fatalf("Unpublish succeeded, want conflict error")
	}
	if !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("error = %v, want ErrCapabilityNameConflict", err)
	}
	if !strings.Contains(err.Error(), "redis.cluster.info.read") {
		t.Fatalf("error message %q does not contain capability name", err.Error())
	}
	if !strings.Contains(err.Error(), "draft") {
		t.Fatalf("error message %q does not indicate conflict source is an existing draft", err.Error())
	}
}

func TestManagerRepublishAfterUnpublishSucceeds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), validReadYAML("published"))
	manager := capabilities.NewManager(root, nil)

	// 下线：published → discovered
	unpublished, err := manager.Unpublish(context.Background(), "minio.bucket.capacity.read")
	if err != nil {
		t.Fatalf("Unpublish returned %v", err)
	}
	if unpublished.Source != "discovered" || unpublished.Status != "needs_review" {
		t.Fatalf("after unpublish: %+v, want discovered needs_review", unpublished)
	}
	if _, err := os.Stat(filepath.Join(root, "published", "minio.bucket.capacity.read.yaml")); !os.IsNotExist(err) {
		t.Fatalf("published file should be removed after unpublish, stat = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml")); err != nil {
		t.Fatalf("discovered file should exist after unpublish, stat = %v", err)
	}

	// 再次发布：discovered → published
	republished, err := manager.Publish(context.Background(), "minio.bucket.capacity.read")
	if err != nil {
		t.Fatalf("Publish after unpublish returned %v", err)
	}
	if republished.Source != "published" || republished.Status != "published" {
		t.Fatalf("after republish: %+v, want published", republished)
	}
	if _, err := os.Stat(filepath.Join(root, "published", "minio.bucket.capacity.read.yaml")); err != nil {
		t.Fatalf("published file should exist after republish, stat = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml")); !os.IsNotExist(err) {
		t.Fatalf("discovered file should be removed after republish, stat = %v", err)
	}
}

func TestManagerTestExecutesReadCapabilityAndReturnsNormalizedResult(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/minio/clusters/m1/buckets/archive/capacity" {
			t.Fatalf("path = %q, want capacity endpoint", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{"usage_pct": 77}})
	}))
	defer server.Close()

	manager := capabilities.NewManager(t.TempDir(), capabilities.NewHTTPAdapter(server.Client()))
	capability := managedReadCapability("minio.bucket.capacity.read", "needs_review")
	capability.Backend.BaseURL = server.URL
	result, err := manager.Test(context.Background(), capability, map[string]any{"cluster": "m1", "bucket": "archive"})
	if err != nil {
		t.Fatalf("Test returned %v", err)
	}
	if result.Summary != "Bucket archive usage is 77%" || result.Data["usage_pct"] == nil {
		t.Fatalf("result = %+v, want normalized preview", result)
	}

	writeCapability := managedReadCapability("minio.bucket.capacity.set", "needs_review")
	writeCapability.Operation = tools.Write
	writeCapability.Backend.Method = http.MethodPost
	if _, err := manager.Test(context.Background(), writeCapability, map[string]any{"cluster": "m1", "bucket": "archive"}); !errors.Is(err, capabilities.ErrTestRequiresRead) {
		t.Fatalf("write test error = %v, want ErrTestRequiresRead", err)
	}
}

func TestManagerImportOpenAPIFromURLWritesDiscoveredDrafts(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	specServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/api-docs" {
			t.Fatalf("path = %q, want OpenAPI endpoint", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`openapi: 3.0.0
paths:
  /api/minio/clusters/{cluster}/buckets/{bucket}/capacity:
    get:
      tags: [minio]
      summary: Bucket capacity
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: bucket
          in: path
          required: true
          schema: {type: string}
`))
	}))
	defer specServer.Close()
	root := t.TempDir()
	manager := capabilities.NewManager(root, capabilities.NewHTTPAdapter(specServer.Client()))

	imported, err := manager.ImportOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLImportRequest{
		OpenAPIURL:     specServer.URL + "/v3/api-docs",
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("ImportOpenAPIFromURL returned %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("imported = %+v, want one draft", imported)
	}
	if imported[0].Name != "minio.bucket.capacity.read" || imported[0].Source != capabilities.SourceDiscovered || imported[0].Backend.BaseURL != "https://middleware.example.com" {
		t.Fatalf("imported = %+v, want discovered draft with backend base URL", imported[0])
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml")); err != nil {
		t.Fatalf("imported draft file missing: %v", err)
	}
}

func TestManagerImportOpenAPIFromURLRejectsUnsupportedURL(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), nil)

	_, err := manager.ImportOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLImportRequest{
		OpenAPIURL:     "file:///tmp/openapi.yaml",
		BackendBaseURL: "https://middleware.example.com",
	})
	if !errors.Is(err, capabilities.ErrInvalidOpenAPIURL) {
		t.Fatalf("ImportOpenAPIFromURL error = %v, want ErrInvalidOpenAPIURL", err)
	}
}

func TestManagerPreviewOpenAPIFromURLDoesNotWriteDrafts(t *testing.T) {
	t.Parallel()
	ensureImporterTestDomains(t)
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`))
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))

	preview, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("PreviewOpenAPIFromURL returned %v", err)
	}

	if preview.Source.OpenAPIURL != server.URL || preview.Source.BackendBaseURL != "https://middleware.example.com" {
		t.Fatalf("source = %+v, want request source", preview.Source)
	}
	if preview.Stats.Total != 1 || preview.Stats.Recommended != 1 {
		t.Fatalf("stats = %+v, want one recommended candidate", preview.Stats)
	}
	if _, err := os.Stat(filepath.Join(dir, capabilities.SourceDiscovered)); !os.IsNotExist(err) {
		t.Fatalf("discovered dir stat err = %v, want no drafts written", err)
	}
}

func TestManagerCommitOpenAPIFromURLWritesOnlySelectedCandidates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
  /api/kafka/{cluster}/topics/{topic}/retention:
    post:
      operationId: setKafkaTopicRetention
      tags: [kafka]
      summary: Set topic retention
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))
	preview, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("PreviewOpenAPIFromURL returned %v", err)
	}

	result, err := manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Fingerprint:    preview.Source.Fingerprint,
		Selections: []capabilities.OpenAPIURLCommitSelection{{
			CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
			Overrides: capabilities.ImportCandidateOverride{
				Name:         "minio.bucket.capacity.read",
				Domain:       "minio",
				ResourceType: "bucket",
				Operation:    tools.Read,
				Risk:         tools.Low,
			},
		}},
	})
	if err != nil {
		t.Fatalf("CommitOpenAPIFromURL returned %v", err)
	}

	if len(result.Capabilities) != 1 || result.Capabilities[0].Name != "minio.bucket.capacity.read" {
		t.Fatalf("capabilities = %+v, want selected minio draft only", result.Capabilities)
	}
	if result.Capabilities[0].Backend.BaseURL != "https://middleware.example.com" {
		t.Fatalf("base url = %q", result.Capabilities[0].Backend.BaseURL)
	}
	if _, err := manager.Get(context.Background(), "minio.bucket.capacity.read"); err != nil {
		t.Fatalf("selected draft not saved: %v", err)
	}
	if _, err := manager.Get(context.Background(), "kafka.topic.retention.update.setkafkatopicretention"); !errors.Is(err, capabilities.ErrCapabilityNotFound) {
		t.Fatalf("unselected draft lookup err = %v, want ErrCapabilityNotFound", err)
	}
}

func TestManagerCommitOpenAPIFromURLRejectsChangedFingerprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))

	_, err := manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Fingerprint:    "sha256:changed",
		Selections: []capabilities.OpenAPIURLCommitSelection{{
			CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
		}},
	})

	if !errors.Is(err, capabilities.ErrOpenAPIFingerprintChanged) {
		t.Fatalf("CommitOpenAPIFromURL error = %v, want ErrOpenAPIFingerprintChanged", err)
	}
}

func TestManagerCommitOpenAPIFromURLRejectsMissingFingerprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))

	_, err := manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Selections: []capabilities.OpenAPIURLCommitSelection{{
			CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
		}},
	})

	if !errors.Is(err, capabilities.ErrInvalidOpenAPIFingerprint) {
		t.Fatalf("CommitOpenAPIFromURL error = %v, want ErrInvalidOpenAPIFingerprint", err)
	}
	if got := discoveredEntryCount(t, dir); got != 0 {
		t.Fatalf("discovered entries = %d, want none", got)
	}
}

func TestManagerCommitOpenAPIFromURLRejectsDuplicateSelectionNames(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
  /api/kafka/{cluster}/consumer-groups/{group}/lag:
    get:
      operationId: getKafkaConsumerLag
      tags: [kafka]
      summary: Read consumer lag
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))
	preview, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("PreviewOpenAPIFromURL returned %v", err)
	}

	_, err = manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Fingerprint:    preview.Source.Fingerprint,
		Selections: []capabilities.OpenAPIURLCommitSelection{
			{
				CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity",
				Overrides:   capabilities.ImportCandidateOverride{Name: "middleware.duplicate.read"},
			},
			{
				CandidateID: "GET /api/kafka/{cluster}/consumer-groups/{group}/lag",
				Overrides:   capabilities.ImportCandidateOverride{Name: "middleware.duplicate.read"},
			},
		},
	})

	if !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("CommitOpenAPIFromURL error = %v, want ErrCapabilityNameConflict", err)
	}
	if !strings.Contains(err.Error(), "middleware.duplicate.read") {
		t.Fatalf("error message %q does not contain conflicting capability name", err.Error())
	}
	if !strings.Contains(err.Error(), "batch") {
		t.Fatalf("error message %q does not indicate conflict source is same-batch name collision", err.Error())
	}
	if got := discoveredEntryCount(t, dir); got != 0 {
		t.Fatalf("discovered entries = %d, want none", got)
	}
}

func TestManagerCommitOpenAPIFromURLRejectsDuplicateCandidateSelections(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := []byte(`openapi: 3.0.0
paths:
  /api/minio/{cluster}/buckets/{bucket}/capacity:
    get:
      operationId: getMinioBucketCapacity
      tags: [minio]
      summary: Read bucket capacity
`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client()))
	preview, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("PreviewOpenAPIFromURL returned %v", err)
	}

	_, err = manager.CommitOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLCommitRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
		Fingerprint:    preview.Source.Fingerprint,
		Selections: []capabilities.OpenAPIURLCommitSelection{
			{CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity"},
			{CandidateID: "GET /api/minio/{cluster}/buckets/{bucket}/capacity"},
		},
	})

	if !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("CommitOpenAPIFromURL error = %v, want ErrCapabilityNameConflict", err)
	}
	if !strings.Contains(err.Error(), "GET /api/minio/{cluster}/buckets/{bucket}/capacity") {
		t.Fatalf("error message %q does not contain the duplicated candidate id", err.Error())
	}
	if !strings.Contains(err.Error(), "selected more than once") {
		t.Fatalf("error message %q does not indicate conflict source is duplicate selection", err.Error())
	}
	if got := discoveredEntryCount(t, dir); got != 0 {
		t.Fatalf("discovered entries = %d, want none", got)
	}
}

func TestManagerPreviewOpenAPIFromURLRejectsUnsupportedURL(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), capabilities.NewHTTPAdapter(nil))

	_, err := manager.PreviewOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLPreviewRequest{
		OpenAPIURL:     "file:///tmp/openapi.yaml",
		BackendBaseURL: "https://middleware.example.com",
	})

	if !errors.Is(err, capabilities.ErrInvalidOpenAPIURL) {
		t.Fatalf("PreviewOpenAPIFromURL error = %v, want ErrInvalidOpenAPIURL", err)
	}
}

func discoveredEntryCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "discovered"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read discovered dir: %v", err)
	}
	return len(entries)
}

func managedReadCapability(name, status string) capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          name,
		Status:        status,
		Domain:        "minio",
		ResourceType:  "bucket",
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			BaseURL:   "https://backend.example.com",
			Method:    http.MethodGet,
			Path:      "/api/minio/clusters/{cluster}/buckets/{bucket}/capacity",
			TimeoutMS: 3000,
		},
		InputSchema: map[string]capabilities.InputField{
			"cluster": {Type: "string", Required: true},
			"bucket":  {Type: "string", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "observation",
			SummaryTemplate: "Bucket {bucket} usage is {usage_pct}%",
			Fields:          map[string]string{"usage_pct": "$.data.usage_pct"},
		},
		Auth: capabilities.AuthSpec{
			Roles: []string{"viewer", "operator", "admin"},
		},
	}
}

func validWriteYAMLWithStatus(status string) string {
	return strings.Replace(validWriteYAML(), "status: published", "status: "+status, 1)
}

func TestManagerQuickPublishPublishesReadCapabilityDirectly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager := capabilities.NewManager(root, nil)

	published, err := manager.QuickPublish(context.Background(), capabilities.QuickPublishRequest{
		Name:           "redis.cluster.info.read",
		Domain:         "redis",
		ResourceType:   "cluster",
		BackendBaseURL: "https://middleware.example.com",
		Method:         http.MethodGet,
		Path:           "/api/redis/clusters/{cluster}/info",
		Description:    "Read Redis cluster info",
	})
	if err != nil {
		t.Fatalf("QuickPublish returned %v", err)
	}
	if published.Source != "published" || published.Status != "published" {
		t.Fatalf("published = %+v, want published item", published)
	}
	if _, err := os.Stat(filepath.Join(root, "discovered", "redis.cluster.info.read.yaml")); !os.IsNotExist(err) {
		t.Fatalf("discovered draft exists, stat = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "published", "redis.cluster.info.read.yaml")); err != nil {
		t.Fatalf("published file missing: %v", err)
	}
	if _, ok := published.InputSchema["environment"]; ok {
		t.Fatalf("input_schema unexpectedly contains environment (concept removed), got %+v", published.InputSchema)
	}
	if field, ok := published.InputSchema["cluster"]; !ok || !field.Required || field.Type != "string" {
		t.Fatalf("input_schema missing required cluster string variable, got %+v", published.InputSchema)
	}
	if published.Backend.BaseURL != "https://middleware.example.com" || published.Backend.Method != http.MethodGet {
		t.Fatalf("backend = %+v, want absolute GET", published.Backend)
	}
	if published.Operation != tools.Read || published.Risk != tools.Low {
		t.Fatalf("operation=%q risk=%q, want read/low", published.Operation, published.Risk)
	}
	if published.AI.Description != "Read Redis cluster info" {
		t.Fatalf("ai.description = %q", published.AI.Description)
	}
}

func TestManagerQuickPublishRejectsUnsupportedMethod(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), nil)

	_, err := manager.QuickPublish(context.Background(), capabilities.QuickPublishRequest{
		Name:           "redis.cluster.info.read",
		Domain:         "redis",
		ResourceType:   "cluster",
		BackendBaseURL: "https://middleware.example.com",
		Method:         "OPTIONS",
		Path:           "/api/redis/clusters/{cluster}/info",
		Description:    "Read Redis cluster info",
	})
	if !errors.Is(err, capabilities.ErrInvalidQuickPublishRequest) {
		t.Fatalf("error = %v, want ErrInvalidQuickPublishRequest", err)
	}
}

func TestManagerQuickPublishRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), nil)

	_, err := manager.QuickPublish(context.Background(), capabilities.QuickPublishRequest{
		Name:           "redis.cluster.info.read",
		Domain:         "redis",
		ResourceType:   "cluster",
		BackendBaseURL: "middleware.example.com",
		Method:         http.MethodGet,
		Path:           "/api/redis/clusters/{cluster}/info",
		Description:    "Read Redis cluster info",
	})
	if !errors.Is(err, capabilities.ErrInvalidQuickPublishBaseURL) {
		t.Fatalf("error = %v, want ErrInvalidQuickPublishBaseURL", err)
	}
}

func TestManagerQuickPublishRejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()
	manager := capabilities.NewManager(t.TempDir(), nil)

	_, err := manager.QuickPublish(context.Background(), capabilities.QuickPublishRequest{
		Method: http.MethodGet,
	})
	if !errors.Is(err, capabilities.ErrInvalidQuickPublishRequest) {
		t.Fatalf("error = %v, want ErrInvalidQuickPublishRequest", err)
	}
}

func TestManagerQuickPublishRejectsNameConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "redis.cluster.info.read.yaml"), strings.Replace(validReadYAML("published"), "name: minio.bucket.capacity.read", "name: redis.cluster.info.read", 1))
	manager := capabilities.NewManager(root, nil)

	_, err := manager.QuickPublish(context.Background(), capabilities.QuickPublishRequest{
		Name:           "redis.cluster.info.read",
		Domain:         "redis",
		ResourceType:   "cluster",
		BackendBaseURL: "https://middleware.example.com",
		Method:         http.MethodGet,
		Path:           "/api/redis/clusters/{cluster}/info",
		Description:    "Read Redis cluster info",
	})
	if !errors.Is(err, capabilities.ErrCapabilityNameConflict) {
		t.Fatalf("error = %v, want ErrCapabilityNameConflict", err)
	}
}

// staticEnricher 是把所有草稿字段加上中文说明/示例的测试富化器，验证 Manager
// 装配 WithEnricher 后，手动 EnrichDraft 会回填字段。
type staticEnricher struct{}

func (staticEnricher) Enrich(_ context.Context, drafts []capabilities.Capability) ([]capabilities.Capability, error) {
	out := make([]capabilities.Capability, len(drafts))
	for i, d := range drafts {
		sc := map[string]capabilities.InputField{}
		for name, f := range d.InputSchema {
			f.Description = "参数 " + name
			f.Examples = []string{"ex-" + name}
			sc[name] = f
		}
		d.InputSchema = sc
		out[i] = d
	}
	return out, nil
}

func TestManagerEnrichDraftManualTrigger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`openapi: 3.0.0
paths:
  /api/kafka/{cluster}/topics/{topic}/retention:
    post:
      operationId: setTopicRetention
      tags: [kafka]
      summary: Set topic retention
      parameters:
        - name: cluster
          in: path
          required: true
          schema: {type: string}
        - name: topic
          in: path
          required: true
          schema: {type: string}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [retention_hours]
              properties:
                retention_hours:
                  type: integer
`))
	}))
	defer server.Close()
	manager := capabilities.NewManager(dir, capabilities.NewHTTPAdapter(server.Client())).
		WithEnricher(staticEnricher{})

	imported, err := manager.ImportOpenAPIFromURL(context.Background(), capabilities.OpenAPIURLImportRequest{
		OpenAPIURL:     server.URL,
		BackendBaseURL: "https://middleware.example.com",
	})
	if err != nil {
		t.Fatalf("ImportOpenAPIFromURL returned %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("imported = %d, want 1", len(imported))
	}
	// 导入路径不碰 LLM 富化：草稿是规则推断的原始输入字段。
	if imported[0].Capability.InputSchema["cluster"].Description != "" {
		t.Fatalf("import should not enrich: %+v", imported[0].Capability.InputSchema)
	}
	// 手动触发富化后回填。
	enriched, err := manager.EnrichDraft(context.Background(), imported[0].Name)
	if err != nil {
		t.Fatalf("EnrichDraft returned %v", err)
	}
	if enriched.Capability.InputSchema["cluster"].Description != "参数 cluster" || enriched.Capability.InputSchema["cluster"].Examples[0] != "ex-cluster" {
		t.Fatalf("cluster input not enriched: %+v", enriched.Capability.InputSchema["cluster"])
	}
	if enriched.Capability.InputSchema["retention_hours"].Description != "参数 retention_hours" {
		t.Fatalf("retention_hours input not enriched: %+v", enriched.Capability.InputSchema["retention_hours"])
	}
}

// staticRenameEnricher 批量富化时给每个草稿建议更名 + 重写描述，验证改名持久化。
type staticRenameEnricher struct{}

func (staticRenameEnricher) Enrich(_ context.Context, drafts []capabilities.Capability) ([]capabilities.Capability, error) {
	out := make([]capabilities.Capability, len(drafts))
	for i, d := range drafts {
		d.Name = "refined." + d.Name
		d.AI.Description = "精修描述 " + d.Name
		out[i] = d
	}
	return out, nil
}

func TestManagerEnrichDraftsBatchRenamesAndPersists(t *testing.T) {
	dir := t.TempDir()
	manager := capabilities.NewManager(dir, nil).WithEnricher(staticRenameEnricher{})
	ctx := context.Background()
	base := func(name string) capabilities.Capability {
		return capabilities.Capability{
			Name:         name,
			Status:       capabilities.StatusNeedsReview,
			Domain:       "unknown",
			ResourceType: "resource",
			Operation:    tools.Read,
			Risk:         tools.Low,
			Backend:      capabilities.BackendSpec{Adapter: "http", Method: "GET", Path: "/api/weather/" + name, BaseURL: "https://w.example.com", TimeoutMS: 3000},
			InputSchema:  map[string]capabilities.InputField{"city": {Type: "string", Required: true}},
			Output:       capabilities.OutputSpec{Kind: "observation", SummaryTemplate: "ok", Fields: map[string]string{"status": "$.status"}},
			Auth:         capabilities.AuthSpec{Roles: []string{"viewer", "operator", "admin"}},
			AI:           capabilities.AISpec{Description: "old " + name},
		}
	}
	if _, err := manager.SaveDraft(ctx, base("unknown.a.read")); err != nil {
		t.Fatalf("save draft a: %v", err)
	}
	if _, err := manager.SaveDraft(ctx, base("unknown.b.read")); err != nil {
		t.Fatalf("save draft b: %v", err)
	}

	out, err := manager.EnrichDraftsBatch(ctx, []string{"unknown.a.read", "unknown.b.read"})
	if err != nil {
		t.Fatalf("EnrichDraftsBatch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("out = %d, want 2", len(out))
	}
	if out[0].Name != "refined.unknown.a.read" || out[1].Name != "refined.unknown.b.read" {
		t.Fatalf("names = %s %s, want refined names", out[0].Name, out[1].Name)
	}
	if !strings.Contains(out[0].Capability.AI.Description, "精修描述") {
		t.Fatalf("description not enriched: %q", out[0].Capability.AI.Description)
	}
	// 旧名草稿应已被删除（改名持久化）
	if _, err := manager.Get(ctx, "unknown.a.read"); !errors.Is(err, capabilities.ErrCapabilityNotFound) {
		t.Fatalf("old draft a still exists: %v", err)
	}
	if _, err := manager.Get(ctx, "unknown.b.read"); !errors.Is(err, capabilities.ErrCapabilityNotFound) {
		t.Fatalf("old draft b still exists: %v", err)
	}
	// 新名可查
	if _, err := manager.Get(ctx, "refined.unknown.a.read"); err != nil {
		t.Fatalf("new draft a not found: %v", err)
	}
}

func TestManagerEnrichDraftsBatchConflictKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	manager := capabilities.NewManager(dir, nil).WithEnricher(staticRenameEnricher{})
	ctx := context.Background()
	base := func(name string) capabilities.Capability {
		return capabilities.Capability{
			Name: name, Status: capabilities.StatusNeedsReview, Domain: "unknown", ResourceType: "resource",
			Operation: tools.Read, Risk: tools.Low,
			Backend:      capabilities.BackendSpec{Adapter: "http", Method: "GET", Path: "/api/weather/" + name, BaseURL: "https://w.example.com", TimeoutMS: 3000},
			InputSchema:  map[string]capabilities.InputField{"city": {Type: "string", Required: true}},
			Output:       capabilities.OutputSpec{Kind: "observation", SummaryTemplate: "ok", Fields: map[string]string{"status": "$.status"}},
			Auth:         capabilities.AuthSpec{Roles: []string{"viewer", "operator", "admin"}},
			AI:           capabilities.AISpec{Description: "old " + name},
		}
	}
	// 预先存在一个草稿 refined.unknown.a.read，会与新名冲突
	if _, err := manager.SaveDraft(ctx, base("refined.unknown.a.read")); err != nil {
		t.Fatalf("save conflict draft: %v", err)
	}
	if _, err := manager.SaveDraft(ctx, base("unknown.a.read")); err != nil {
		t.Fatalf("save draft a: %v", err)
	}
	out, err := manager.EnrichDraftsBatch(ctx, []string{"unknown.a.read"})
	if err != nil {
		t.Fatalf("EnrichDraftsBatch: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("out = %d, want 1", len(out))
	}
	// 冲突时回退原名，不覆盖已有草稿
	if out[0].Name != "unknown.a.read" {
		t.Fatalf("name = %q, want original kept on conflict", out[0].Name)
	}
	if _, err := manager.Get(ctx, "refined.unknown.a.read"); err != nil {
		t.Fatalf("pre-existing draft clobbered: %v", err)
	}
}

func TestManagerUnpublishCleansUpPublishedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "published", "minio.bucket.capacity.read.yaml"), validReadYAML("published"))
	manager := capabilities.NewManager(root, nil)

	unpublished, err := manager.Unpublish(context.Background(), "minio.bucket.capacity.read")
	if err != nil {
		t.Fatalf("Unpublish returned %v", err)
	}
	if unpublished.Source != "discovered" || unpublished.Status != "needs_review" {
		t.Fatalf("unpublished = %+v, want discovered needs_review", unpublished)
	}

	if _, err := os.Stat(filepath.Join(root, "published", "minio.bucket.capacity.read.yaml")); !os.IsNotExist(err) {
		t.Fatalf("published file was not cleaned up, stat = %v", err)
	}
	draftPath := filepath.Join(root, "discovered", "minio.bucket.capacity.read.yaml")
	draftData, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatalf("discovered draft missing after unpublish: %v", err)
	}
	if strings.Contains(string(draftData), "status: published") {
		t.Fatalf("discovered draft still contains published status:\n%s", draftData)
	}
	if !strings.Contains(string(draftData), "status: needs_review") {
		t.Fatalf("discovered draft does not contain needs_review status:\n%s", draftData)
	}
}
