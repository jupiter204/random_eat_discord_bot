package db

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func newTestRepo(t *testing.T) Repository {
	t.Helper()
	sqlDB, err := InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return NewRepository(sqlDB)
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestUpsertPreference_PartialUpdate(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	pref, err := repo.UpsertPreference(ctx, "u1", 25.0, 121.0, nil, nil)
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	if pref.LocationName != "" || pref.Radius != DefaultRadius {
		t.Fatalf("unexpected defaults on insert: %+v", pref)
	}

	if _, err := repo.UpsertPreference(ctx, "u1", 25.0, 121.0, strPtr("公司"), intPtr(2500)); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	pref, err = repo.UpsertPreference(ctx, "u1", 24.5, 120.5, nil, nil)
	if err != nil {
		t.Fatalf("coordinate-only upsert failed: %v", err)
	}
	if pref.LocationName != "公司" || pref.Radius != 2500 || pref.Latitude != 24.5 || pref.Longitude != 120.5 {
		t.Errorf("coordinate-only update must keep name and radius, got %+v", pref)
	}

	// 明確傳入空字串代表清除別名
	pref, _ = repo.UpsertPreference(ctx, "u1", 24.5, 120.5, strPtr(""), nil)
	if pref.LocationName != "" || pref.Radius != 2500 {
		t.Errorf("empty name should clear alias and keep radius, got %+v", pref)
	}

	got, err := repo.GetPreference(ctx, "u1")
	if err != nil || got == nil || got.Radius != 2500 || got.LocationName != "" {
		t.Errorf("GetPreference mismatch: %+v, %v", got, err)
	}
}

func TestUpsertPreference_ConcurrentPartialUpdatesDoNotClobber(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if _, err := repo.UpsertPreference(ctx, "u1", 25.0, 121.0, nil, nil); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = repo.UpsertPreference(ctx, "u1", 25.0, 121.0, nil, intPtr(2500))
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = repo.UpsertPreference(ctx, "u1", 25.0, 121.0, strPtr("公司"), nil)
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent upsert %d failed: %v", i, err)
		}
	}

	got, err := repo.GetPreference(ctx, "u1")
	if err != nil || got == nil {
		t.Fatalf("GetPreference failed: %v (%+v)", err, got)
	}
	if got.Radius != 2500 || got.LocationName != "公司" {
		t.Errorf("concurrent partial updates clobbered each other: %+v", got)
	}
}

func TestDeletePreference(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	_, _ = repo.UpsertPreference(ctx, "u1", 25.0, 121.0, nil, nil)
	if err := repo.DeletePreference(ctx, "u1"); err != nil {
		t.Fatalf("DeletePreference failed: %v", err)
	}
	if got, _ := repo.GetPreference(ctx, "u1"); got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}
