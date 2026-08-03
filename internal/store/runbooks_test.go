package store

import (
	"context"
	"errors"
	"testing"
)

func sampleRunbook() Runbook {
	return Runbook{
		Slug:            "kafka-retention-low-risk",
		Name:            "Kafka 保留时间调整",
		IntentPattern:   []string{"保留", "retention"},
		ToolSequence:    []string{"topic.retention.set"},
		DefaultStrategy: &RunbookStrategy{TimeoutMS: 60000, RiskLevel: "low"},
		RiskLevel:       "low",
		IsBuiltin:       true,
		IsEnabled:       true,
	}
}

func TestMemoryRunbookStoreLifecycle(t *testing.T) {
	t.Parallel()
	s := NewMemoryRunbookStore()
	ctx := context.Background()

	// Create
	created, err := s.CreateRunbook(ctx, sampleRunbook())
	if err != nil {
		t.Fatalf("CreateRunbook: %v", err)
	}
	if created.ID == "" {
		t.Fatal("ID not generated")
	}
	if len(created.IntentPattern) != 2 || len(created.ToolSequence) != 1 {
		t.Fatalf("slices not preserved: %+v", created)
	}
	if created.DefaultStrategy == nil || created.DefaultStrategy.TimeoutMS != 60000 {
		t.Fatalf("strategy not preserved: %+v", created.DefaultStrategy)
	}

	// Get by slug
	got, err := s.GetRunbook(ctx, "kafka-retention-low-risk")
	if err != nil {
		t.Fatalf("GetRunbook: %v", err)
	}
	if got.Slug != "kafka-retention-low-risk" {
		t.Fatalf("slug = %q", got.Slug)
	}

	// Get missing
	if _, err := s.GetRunbook(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRunbook(missing) = %v, want ErrNotFound", err)
	}

	// Create duplicate slug -> ErrConflict
	if _, err := s.CreateRunbook(ctx, sampleRunbook()); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateRunbook(dup) = %v, want ErrConflict", err)
	}

	// ListEnabled filters
	disabled := sampleRunbook()
	disabled.Slug = "disabled-rb"
	disabled.IsEnabled = false
	if _, err := s.CreateRunbook(ctx, disabled); err != nil {
		t.Fatalf("CreateRunbook disabled: %v", err)
	}
	enabled, err := s.ListEnabledRunbooks(ctx)
	if err != nil {
		t.Fatalf("ListEnabledRunbooks: %v", err)
	}
	if len(enabled) != 1 || enabled[0].Slug != "kafka-retention-low-risk" {
		t.Fatalf("enabled runbooks = %+v, want 1", enabled)
	}

	// Update
	created.Name = "更新后的名称"
	if _, err := s.UpdateRunbook(ctx, created); err != nil {
		t.Fatalf("UpdateRunbook: %v", err)
	}

	// Delete
	if err := s.DeleteRunbook(ctx, created.ID); err != nil {
		t.Fatalf("DeleteRunbook: %v", err)
	}
	if _, err := s.GetRunbook(ctx, "kafka-retention-low-risk"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRunbook after delete = %v, want ErrNotFound", err)
	}
}

func TestSQLRunbookStoreLifecycle(t *testing.T) {
	t.Parallel()
	db := testSQLite(t)
	if err := ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	s := NewSQLRunbookStore(db)
	ctx := context.Background()

	created, err := s.CreateRunbook(ctx, sampleRunbook())
	if err != nil {
		t.Fatalf("CreateRunbook: %v", err)
	}
	if created.ID == "" {
		t.Fatal("ID not generated")
	}

	got, err := s.GetRunbook(ctx, "kafka-retention-low-risk")
	if err != nil {
		t.Fatalf("GetRunbook: %v", err)
	}
	if len(got.IntentPattern) != 2 || got.ToolSequence[0] != "topic.retention.set" {
		t.Fatalf("slices not round-tripped: %+v", got)
	}
	if got.DefaultStrategy == nil || got.DefaultStrategy.TimeoutMS != 60000 {
		t.Fatalf("strategy not round-tripped: %+v", got.DefaultStrategy)
	}

	enabled, err := s.ListEnabledRunbooks(ctx)
	if err != nil {
		t.Fatalf("ListEnabledRunbooks: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("enabled = %d, want 1", len(enabled))
	}
}

func TestSeedBuiltinRunbooksIdempotent(t *testing.T) {
	t.Parallel()
	s := NewMemoryRunbookStore()
	ctx := context.Background()

	if err := SeedBuiltinRunbooks(ctx, s); err != nil {
		t.Fatalf("SeedBuiltinRunbooks first: %v", err)
	}
	first, err := s.ListRunbooks(ctx)
	if err != nil {
		t.Fatalf("ListRunbooks: %v", err)
	}
	if len(first) != len(builtinRunbooks) {
		t.Fatalf("seeded = %d, want %d", len(first), len(builtinRunbooks))
	}
	for _, rb := range first {
		if len(rb.ToolSequence) == 0 {
			t.Errorf("runbook %q has no tool sequence", rb.Slug)
		}
	}

	if err := SeedBuiltinRunbooks(ctx, s); err != nil {
		t.Fatalf("SeedBuiltinRunbooks second: %v", err)
	}
	second, _ := s.ListRunbooks(ctx)
	if len(second) != len(first) {
		t.Fatalf("second seed changed count: %d -> %d (not idempotent)", len(first), len(second))
	}
}
