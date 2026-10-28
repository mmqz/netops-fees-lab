package repo

import "sort"

// migrationV40 adds the site master-data columns of issue #38 (schema
// version 40): site_nature / power_mode / lifecycle — the three
// structured answers the cross-dataset reconcile needs to attribute an
// only_tower / only_electricity row instead of merely listing it (the
// 实测 shape: 36% of the tower sites had no electricity record and the
// system could not tell 共享站 from 已退网仍计费).
//
// The geo precedent (migration v20) applies: REAL columns beside the
// attributes JSON, because free-text attributes cannot be constrained or
// joined. The columns carry the ENGLISH tokens (self_built/shared/leased,
// direct/transfer/none, active/retired/dismantled) behind CHECK
// constraints; the attributes JSON keeps the raw sheet text (lossless
// source of truth); the importer (execute.go's #38 branch) projects the
// recognized Chinese synonyms onto the columns, and an unknown value
// warns and leaves the column NULL — never a row failure, never a guess.
//
// Backfill: the v20 pattern — CASE translations over json_extract, so a
// database whose resources ALREADY carry 站址性质/供电方式/生命周期 attribute
// values (imported before the structured channel existed) gains the
// columns on upgrade. Values outside the closed sets stay NULL (the
// attributes text is untouched) — an out-of-set value is exactly the
// "master data missing" state the reconcile reports honestly.
//
// Purely additive: three ADD COLUMNs, no index (the reconcile reads the
// three columns by resource id through the confirmed link, never by range
// scan), foreign_keys enforcement stays ON.
var migrationV40 = Migration{
	Version: 40,
	Name:    "site master data columns (#38 cross-reconcile attribution)",
	SQL: `
ALTER TABLE resources ADD COLUMN site_nature TEXT
  CHECK (site_nature IS NULL OR site_nature IN ('self_built','shared','leased'));
ALTER TABLE resources ADD COLUMN power_mode TEXT
  CHECK (power_mode IS NULL OR power_mode IN ('direct','transfer','none'));
ALTER TABLE resources ADD COLUMN lifecycle TEXT
  CHECK (lifecycle IS NULL OR lifecycle IN ('active','retired','dismantled'));
UPDATE resources SET site_nature = CASE json_extract(attributes, '$.site_nature')
    WHEN '自建' THEN 'self_built' WHEN 'self_built' THEN 'self_built'
    WHEN '共享' THEN 'shared'     WHEN 'shared' THEN 'shared'
    WHEN '租用' THEN 'leased'     WHEN 'leased' THEN 'leased'
    ELSE NULL END
  WHERE json_extract(attributes, '$.site_nature') IS NOT NULL;
UPDATE resources SET power_mode = CASE json_extract(attributes, '$.power_mode')
    WHEN '直供' THEN 'direct'   WHEN 'direct' THEN 'direct'
    WHEN '转供' THEN 'transfer' WHEN 'transfer' THEN 'transfer'
    WHEN '无' THEN 'none'       WHEN 'none' THEN 'none'
    ELSE NULL END
  WHERE json_extract(attributes, '$.power_mode') IS NOT NULL;
UPDATE resources SET lifecycle = CASE json_extract(attributes, '$.lifecycle')
    WHEN '在网' THEN 'active'      WHEN 'active' THEN 'active'
    WHEN '退网' THEN 'retired'     WHEN 'retired' THEN 'retired'
    WHEN '已拆' THEN 'dismantled'  WHEN 'dismantled' THEN 'dismantled'
    ELSE NULL END
  WHERE json_extract(attributes, '$.lifecycle') IS NOT NULL;
`,
}

func init() {
	migrations = append(migrations, migrationV40)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
