package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func inspectionReportRouter(t *testing.T, reportStore store.InspectionReportStore) http.Handler {
	t.Helper()
	return httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
		httpapi.WithInspectionReports(reportStore),
	)
}

func sampleInspectionReport() store.InspectionReport {
	return store.InspectionReport{
		ID:             "report-1",
		Period:         store.InspectionPeriodDaily,
		WindowStart:    time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		WindowEnd:      time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		GeneratedAt:    time.Date(2026, 8, 1, 0, 30, 0, 0, time.UTC),
		TotalTasks:     2,
		SucceededTasks: 1,
		FailedTasks:    1,
		TaskSummaries: []store.InspectionTaskSummary{
			{TaskID: "t1", TaskName: "Kafka 巡检", CapabilityName: "kafka.status.read", TotalRuns: 2, SucceededRuns: 2, LastStatus: "succeeded"},
		},
		HTMLContent: "<html>report</html>",
	}
}

// TestInspectionReportListReturnsReports verifies GET /v1/inspection-reports
// returns the persisted reports as JSON.
func TestInspectionReportListReturnsReports(t *testing.T) {
	t.Parallel()
	reportStore := &store.MemoryInspectionReportStore{}
	_, _ = reportStore.CreateReport(context.Background(), sampleInspectionReport())

	router := inspectionReportRouter(t, reportStore)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/inspection-reports", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	var reports []store.InspectionReport
	if err := json.Unmarshal(res.Body.Bytes(), &reports); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, res.Body.String())
	}
	if len(reports) != 1 {
		t.Fatalf("reports len = %d, want 1", len(reports))
	}
	if reports[0].ID != "report-1" {
		t.Fatalf("report ID = %q, want report-1", reports[0].ID)
	}
}

// TestInspectionReportGetReturnsReport verifies GET /v1/inspection-reports/{id}
// returns the full report including HTML content.
func TestInspectionReportGetReturnsReport(t *testing.T) {
	t.Parallel()
	reportStore := &store.MemoryInspectionReportStore{}
	_, _ = reportStore.CreateReport(context.Background(), sampleInspectionReport())

	router := inspectionReportRouter(t, reportStore)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/inspection-reports/report-1", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"id":"report-1"`) {
		t.Fatalf("body missing report id: %s", body)
	}
	var report store.InspectionReport
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if report.HTMLContent != "<html>report</html>" {
		t.Fatalf("HTMLContent = %q, want <html>report</html>", report.HTMLContent)
	}
	if len(report.TaskSummaries) != 1 || report.TaskSummaries[0].TaskName != "Kafka 巡检" {
		t.Fatalf("task summaries = %+v, want Kafka 巡检", report.TaskSummaries)
	}
}

// TestInspectionReportGetNonExistentReturns404 verifies that requesting a
// non-existent report returns 404.
func TestInspectionReportGetNonExistentReturns404(t *testing.T) {
	t.Parallel()
	reportStore := &store.MemoryInspectionReportStore{}
	router := inspectionReportRouter(t, reportStore)

	req := signedRequestWithMethod(t, http.MethodGet, "/v1/inspection-reports/nonexistent", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.Code)
	}
}

// TestInspectionReportNotConfiguredReturns500 verifies that the endpoint
// returns 500 when no report store is wired.
func TestInspectionReportNotConfiguredReturns500(t *testing.T) {
	t.Parallel()
	router := httpapi.NewRouter(
		httpapi.NewHMACAuthenticator([]byte("test-secret")),
		nil,
	)
	req := signedRequestWithMethod(t, http.MethodGet, "/v1/inspection-reports", "", "viewer-1", []string{"viewer"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.Code)
	}
}
