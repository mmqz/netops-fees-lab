package repo

// migrationV20 promotes geo coordinates to first-class REAL columns of the
// resources table (schema version 20, W30 — issue #45 / audit-v0.8.0 §2
// field-thickness finding, third item):
//
//   - geo_lat / geo_lng REAL columns, NULL for resources without coordinates.
//     Until now coordinates lived only inside the attributes JSON as STRINGS
//     ({"geo_lat":"48.45402"}), which blocks SQL-level spatial work: no
//     index, no range scan, no nearest-neighbor, and every map/analysis view
//     had to parse JSON per row (the audit counted only 90 port resources
//     carrying them — devices kept theirs in attributes too, and
//     virtual_devices had numeric columns all along: the two layers were
//     structurally inconsistent).
//
//   - Backfill: json_extract re-derives the columns from existing
//     attributes rows (modernc.org/sqlite ships JSON1 — verified in
//     migrations_v20_test with pre-v20 data). Rows without geo keys stay
//     NULL. The combined 经纬度 form never reaches storage: the importer
//     splits it into geo_lat/geo_lng attribute keys before insert
//     (splitGeoCombined), so the backfill reads the split keys only.
//
//   - idx_resources_geo (geo_lat, geo_lng) B-tree: SQLite has no R-Tree over
//     plain tables without a virtual-table rebuild; a two-column B-tree
//     gives the range-scan shape (lat BETWEEN … AND lng BETWEEN …) that
//     ResourceRepo.Nearest uses as its bounding-box prefilter. Exact
//     distance stays in Go (haversine) because the pure-Go driver does not
//     ship the trig math-functions — the honest split: SQL narrows, Go
//     ranks.
//
// Purely additive: two ADD COLUMNs + one CREATE INDEX + one UPDATE, no table
// rebuild, foreign_keys enforcement stays ON (the migrations_v19 style). A
// fresh database skips the backfill UPDATE naturally (no rows). Existing
// (geo_lat,geo_lng) values are unchanged by re-running: the UPDATE is
// idempotent because CAST(json_extract(...) AS REAL) is deterministic and
// the WHERE clause re-selects the same rows.
var migrationV20 = Migration{
	Version: 20,
	Name:    "w30_resource_geo_columns",
	SQL: `
ALTER TABLE resources ADD COLUMN geo_lat REAL;
ALTER TABLE resources ADD COLUMN geo_lng REAL;
CREATE INDEX idx_resources_geo ON resources (geo_lat, geo_lng);
UPDATE resources
   SET geo_lat = CAST(json_extract(attributes, '$.geo_lat') AS REAL),
       geo_lng = CAST(json_extract(attributes, '$.geo_lng') AS REAL)
 WHERE json_extract(attributes, '$.geo_lat') IS NOT NULL
   AND json_extract(attributes, '$.geo_lng') IS NOT NULL;
`,
}

func init() {
	migrations = append(migrations, migrationV20)
}
