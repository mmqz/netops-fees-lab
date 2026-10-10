package repo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// seedDevices inserts n device resources DEV-001..DEV-00n belonging to bat_1
// with an mgmt_ip attribute, returning the created resources.
func seedDevices(t *testing.T, db *DB, n int) []domain.Resource {
	t.Helper()
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	if err := db.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
		t.Fatalf("Create batch: %v", err)
	}
	out := make([]domain.Resource, 0, n)
	for i := 1; i <= n; i++ {
		res := newResource(fmt.Sprintf("res_%03d", i), fmt.Sprintf("DEV-%03d", i), ts)
		res.Attributes["mgmt_ip"] = fmt.Sprintf("10.10.10.%d", i)
		res.Attributes["model"] = fmt.Sprintf("NE40E-%d", i%3)
		if i%5 == 0 {
			res.Status = "停用"
		}
		if err := db.Resources.Insert(ctx, res); err != nil {
			t.Fatalf("Insert resource %d: %v", i, err)
		}
		out = append(out, res)
	}
	return out
}

func TestResourceCRUDRoundtrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seeded := seedDevices(t, db, 3)

	// GetByID with full roundtrip of attributes and timestamps.
	got, err := db.Resources.GetByID(ctx, "res_001")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.NaturalKey != "DEV-001" || got.Type != domain.ResourceTypeDevice {
		t.Errorf("GetByID mismatch: %+v", got)
	}
	if got.Attributes["city"] != "杭州" || got.Attributes["mgmt_ip"] != "10.10.10.1" {
		t.Errorf("attributes roundtrip mismatch: %+v", got.Attributes)
	}
	if !got.CreatedAt.Equal(seeded[0].CreatedAt) || !got.UpdatedAt.Equal(seeded[0].UpdatedAt) {
		t.Errorf("timestamps roundtrip mismatch: %v / %v", got.CreatedAt, got.UpdatedAt)
	}

	// GetByTypeKey (the idempotency probe).
	got, err = db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-002")
	if err != nil || got.ID != "res_002" {
		t.Errorf("GetByTypeKey(DEV-002) = %+v, %v", got, err)
	}
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-999"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByTypeKey(missing) = %v, want domain.ErrNotFound", err)
	}
	if _, err := db.Resources.GetByID(ctx, "res_none"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(missing) = %v, want domain.ErrNotFound", err)
	}

	// Duplicate (type, natural_key) → domain.ErrConflict.
	dup := newResource("res_dup", "DEV-001", time.Now())
	if err := db.Resources.Insert(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("Insert(dup) = %v, want domain.ErrConflict", err)
	}

	// ListByBatch.
	byBatch, err := db.Resources.ListByBatch(ctx, "bat_1")
	if err != nil || len(byBatch) != 3 {
		t.Errorf("ListByBatch = %d items, %v; want 3", len(byBatch), err)
	}

	// DeleteByBatch returns count.
	n, err := db.Resources.DeleteByBatch(ctx, "bat_1")
	if err != nil || n != 3 {
		t.Errorf("DeleteByBatch = %d, %v; want 3", n, err)
	}
	if _, total, _ := db.Resources.Search(ctx, ResourceSearchFilter{}); total != 0 {
		t.Errorf("resources after delete = %d, want 0", total)
	}
}

func TestResourceSearchFiltersAndPagination(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedDevices(t, db, 12)

	// i%3==1 → model NE40E-1 (devices 1, 4, 7, 10).
	withModel := []string{"DEV-001", "DEV-004", "DEV-007", "DEV-010"}
	// i%5==0 → status 停用.
	stopped := []string{"DEV-005", "DEV-010"}

	cases := []struct {
		name  string
		f     ResourceSearchFilter
		want  []string // natural keys in deterministic (type, natural_key) order
		total int
		err   bool // expect an error instead of results
	}{
		{"no filter", ResourceSearchFilter{Limit: 100}, allDeviceKeys(12), 12, false},
		{"type filter", ResourceSearchFilter{Type: domain.ResourceTypeDevice, Limit: 100}, allDeviceKeys(12), 12, false},
		{"invalid type", ResourceSearchFilter{Type: domain.ResourceType("vlan")}, nil, 0, true},
		{"q over natural key, case-insensitive", ResourceSearchFilter{Q: "dev-0", Limit: 100}, allDeviceKeys(12), 12, false},
		{"q prefix dev-00 matches only 001-009", ResourceSearchFilter{Q: "dev-00", Limit: 100}, allDeviceKeys(9), 9, false},
		{"q narrows to one", ResourceSearchFilter{Q: "dev-005", Limit: 100}, []string{"DEV-005"}, 1, false},
		{"q matches name (Chinese, exact contains)", ResourceSearchFilter{Q: "设备-DEV-003", Limit: 100}, []string{"DEV-003"}, 1, false},
		{"q matches attribute value", ResourceSearchFilter{Q: "10.10.10.7", Limit: 100}, []string{"DEV-007"}, 1, false},
		{"q matches attribute value case-insensitively", ResourceSearchFilter{Q: "ne40e-1", Limit: 100}, withModel, len(withModel), false},
		{"status filter", ResourceSearchFilter{Status: "停用", Limit: 100}, stopped, len(stopped), false},
		{"type+status", ResourceSearchFilter{Type: domain.ResourceTypeDevice, Status: "停用", Limit: 100}, stopped, len(stopped), false},
		{"q matching nothing", ResourceSearchFilter{Q: "不存在的查询词", Limit: 100}, nil, 0, false},
	}
	for _, c := range cases {
		got, total, err := db.Resources.Search(ctx, c.f)
		if c.err {
			if err == nil {
				t.Errorf("%s: expected error, got results %v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Search error: %v", c.name, err)
			continue
		}
		if total != c.total {
			t.Errorf("%s: total = %d, want %d", c.name, total, c.total)
		}
		keys := make([]string, len(got))
		for i, res := range got {
			keys[i] = res.NaturalKey
		}
		if fmt.Sprint(keys) != fmt.Sprint(c.want) {
			t.Errorf("%s: keys = %v, want %v", c.name, keys, c.want)
		}
	}

	// Pagination: deterministic order (resource_type, natural_key).
	page1, total, err := db.Resources.Search(ctx, ResourceSearchFilter{Q: "DEV-0", Limit: 5, Offset: 0})
	if err != nil || len(page1) != 5 || total != 12 {
		t.Fatalf("page1: len=%d total=%d err=%v", len(page1), total, err)
	}
	if page1[0].NaturalKey != "DEV-001" || page1[4].NaturalKey != "DEV-005" {
		t.Errorf("page1 keys = %s…%s, want DEV-001…DEV-005", page1[0].NaturalKey, page1[4].NaturalKey)
	}
	page2, _, err := db.Resources.Search(ctx, ResourceSearchFilter{Q: "DEV-0", Limit: 5, Offset: 5})
	if err != nil || len(page2) != 5 || page2[0].NaturalKey != "DEV-006" {
		t.Fatalf("page2: len=%d first=%v err=%v", len(page2), page2[0].NaturalKey, err)
	}
	page3, _, err := db.Resources.Search(ctx, ResourceSearchFilter{Q: "DEV-0", Limit: 5, Offset: 10})
	if err != nil || len(page3) != 2 || page3[0].NaturalKey != "DEV-011" {
		t.Fatalf("page3: len=%d first=%v err=%v", len(page3), page3[0].NaturalKey, err)
	}
	// Offset beyond the end returns an empty page, total unchanged.
	page4, total, err := db.Resources.Search(ctx, ResourceSearchFilter{Q: "DEV-0", Limit: 5, Offset: 50})
	if err != nil || len(page4) != 0 || total != 12 {
		t.Errorf("page4: len=%d total=%d err=%v", len(page4), total, err)
	}
}

// TestResourceSearchEscapesLikeWildcards proves that % and _ in the search
// term match literally instead of acting as LIKE wildcards.
func TestResourceSearchEscapesLikeWildcards(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	// Seed devices first (this also creates batch bat_1) so a wildcard-style
	// match would over-match if escaping were broken (e.g. "DEV-00_" would
	// match DEV-001..DEV-012).
	seedDevices(t, db, 12)
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	wild := newResource("res_wild", "DEV-100%", ts)
	wild.Name = "负载50%设备"
	wild.Attributes = map[string]string{"model": "S9700_24"}
	underscore := newResource("res_und", "DEVX100", ts)
	underscore.Name = "下划线名称"
	plain := newResource("res_plain", "DEV-200", ts)
	for _, r := range []domain.Resource{wild, underscore, plain} {
		if err := db.Resources.Insert(ctx, r); err != nil {
			t.Fatalf("Insert %s: %v", r.ID, err)
		}
	}

	cases := []struct {
		q    string
		want []string
	}{
		{"DEV-100%", []string{"DEV-100%"}}, // % literal, not 0+ chars
		{"DEV-00_", nil},                   // _ literal, not any char
		{"S9700_24", []string{"DEV-100%"}}, // _ literal inside attributes
		{"50%", []string{"DEV-100%"}},      // % literal inside name
		{"DEVX100", []string{"DEVX100"}},
		{"DEV-200", []string{"DEV-200"}},
		{"%", []string{"DEV-100%"}}, // a bare % matches only itself
	}
	for _, c := range cases {
		got, total, err := db.Resources.Search(ctx, ResourceSearchFilter{Q: c.q, Limit: 100})
		if err != nil {
			t.Errorf("q=%q: %v", c.q, err)
			continue
		}
		keys := make([]string, len(got))
		for i, res := range got {
			keys[i] = res.NaturalKey
		}
		if fmt.Sprint(keys) != fmt.Sprint(c.want) || total != len(c.want) {
			t.Errorf("q=%q: keys=%v total=%d, want %v (%d)", c.q, keys, total, c.want, len(c.want))
		}
	}
}

// allDeviceKeys returns DEV-001..DEV-00n.
func allDeviceKeys(n int) []string {
	out := make([]string, n)
	for i := 1; i <= n; i++ {
		out[i-1] = fmt.Sprintf("DEV-%03d", i)
	}
	return out
}

func TestResourceCounts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	if err := db.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
		t.Fatalf("Create batch: %v", err)
	}
	resources := []domain.Resource{
		{ID: "res_1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001", Name: "d1", FirstBatchID: "bat_1", Status: "在网", CreatedAt: ts, UpdatedAt: ts},
		{ID: "res_2", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-002", Name: "d2", FirstBatchID: "bat_1", Status: "停用", CreatedAt: ts, UpdatedAt: ts},
		{ID: "res_3", Type: domain.ResourceTypeIPPool, NaturalKey: "POOL-01", Name: "p1", FirstBatchID: "bat_1", CreatedAt: ts, UpdatedAt: ts},
	}
	for _, r := range resources {
		if err := db.Resources.Insert(ctx, r); err != nil {
			t.Fatalf("Insert %s: %v", r.ID, err)
		}
	}
	byType, err := db.Resources.CountByType(ctx)
	if err != nil {
		t.Fatalf("CountByType: %v", err)
	}
	if byType[domain.ResourceTypeDevice] != 2 || byType[domain.ResourceTypeIPPool] != 1 {
		t.Errorf("CountByType = %v", byType)
	}
	byStatus, err := db.Resources.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if byStatus["在网"] != 1 || byStatus["停用"] != 1 || len(byStatus) != 2 {
		t.Errorf("CountByStatus = %v (NULL status must be excluded)", byStatus)
	}
}

// TestResourceCountByBatchMatchesList pins the COUNT aggregate used by the
// rollback route's delete-response accounting (R2-P3-2): CountByBatch must
// equal the number of resources ListByBatch would load for the same batch.
func TestResourceCountByBatchMatchesList(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedDevices(t, db, 7) // 7 resources of bat_1

	n, err := db.Resources.CountByBatch(ctx, "bat_1")
	if err != nil {
		t.Fatalf("CountByBatch: %v", err)
	}
	listed, err := db.Resources.ListByBatch(ctx, "bat_1")
	if err != nil {
		t.Fatalf("ListByBatch: %v", err)
	}
	if n != len(listed) || n != 7 {
		t.Errorf("CountByBatch = %d, ListByBatch = %d rows, want both 7", n, len(listed))
	}
	if n, err := db.Resources.CountByBatch(ctx, "bat_none"); err != nil || n != 0 {
		t.Errorf("CountByBatch(missing batch) = %d, %v; want 0", n, err)
	}
}
