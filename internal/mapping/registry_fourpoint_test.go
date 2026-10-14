package mapping

import (
	"database/sql"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"

	_ "modernc.org/sqlite" // driver name "sqlite" (read-only sqlite_master access)
)

// TestTypeRegistryFourPointConsistency is the #26 anti-drift gate in its
// final W21 form: the resource TYPE closed set must be IDENTICAL at all
// four places that independently declare it —
//
//	(a) the domain enum (domain.IsValid / AllResourceTypes);
//	(b) the SQL CHECK constraints of a freshly migrated database
//	    (resources.resource_type IN (…) parsed from sqlite_master);
//	(c) the /api/schema projection (BuildSchemaDocument);
//	(d) the web mirror (web/src/schema.ts TYPE_SPECS and TYPE_META keys,
//	    read from the TS SOURCE — the repo-relative path works from this
//	    package's working directory).
//
// Any single-place addition (a new type in enums.go without a spec, a spec
// without a migration CHECK value, a TS mirror entry forgotten) fails here
// instead of drifting silently. The RELATION side has the same four-point
// property (registry ↔ CHECK ↔ RelationTypeFor contract ↔ TS), pinned by
// TestRelationRegistryCoversDomainEnums + TestMigrationRelationsCHECK below.
func TestTypeRegistryFourPointConsistency(t *testing.T) {
	// ---- point (a): domain.IsValid == the Specs type set ----
	specsSet := map[domain.ResourceType]bool{}
	for _, spec := range Specs {
		if specsSet[spec.Type] {
			t.Fatalf("%s: duplicate TypeSpec", spec.Type)
		}
		specsSet[spec.Type] = true
	}
	if len(specsSet) != 33 {
		t.Fatalf("Specs holds %d types, want 33 (8 pre-W21 + 25 W21)", len(specsSet))
	}
	domainSet := map[domain.ResourceType]bool{}
	for _, rt := range domain.AllResourceTypes() {
		if !rt.IsValid() {
			t.Fatalf("AllResourceTypes lists %q which IsValid rejects", rt)
		}
		domainSet[rt] = true
	}
	if len(domainSet) != 33 {
		t.Fatalf("domain declares %d resource types, want 33", len(domainSet))
	}
	for rt := range domainSet {
		if !specsSet[rt] {
			t.Errorf("domain type %q has no TypeSpec (registry drift)", rt)
		}
	}
	for rt := range specsSet {
		if !domainSet[rt] {
			t.Errorf("TypeSpec %q is not in the domain valid set (enum drift)", rt)
		}
	}

	// ---- point (b): SQL CHECK content == the domain set ----
	resourceCHECK, _ := migratedCHECKLists(t)
	if len(resourceCHECK) != 33 {
		t.Errorf("resources CHECK lists %d types, want 33", len(resourceCHECK))
	}
	for _, v := range resourceCHECK {
		if !domainSet[domain.ResourceType(v)] {
			t.Errorf("resources CHECK value %q is not a domain type (migration drift)", v)
		}
	}
	if missing := setDifference(domainSet, resourceCHECK); len(missing) > 0 {
		t.Errorf("resources CHECK misses domain types %v (migration drift)", missing)
	}

	// ---- point (c): /api/schema projection == Specs ----
	doc := BuildSchemaDocument()
	if len(doc.Types) != len(Specs) {
		t.Fatalf("projection has %d types, want %d (len of Specs)", len(doc.Types), len(Specs))
	}
	for i, st := range doc.Types {
		if st.Type != Specs[i].Type {
			t.Errorf("projection types[%d] = %q, want %q (Specs order)", i, st.Type, Specs[i].Type)
		}
		if st.Layer != Specs[i].Layer || st.LayerName != LayerName(Specs[i].Layer) || st.Domain != Specs[i].Domain {
			t.Errorf("%s: projection layer/domain = %d/%q/%q, want %d/%q/%q", st.Type,
				st.Layer, st.LayerName, st.Domain, Specs[i].Layer, LayerName(Specs[i].Layer), Specs[i].Domain)
		}
	}

	// ---- point (d): web mirror (TS source) == Specs ----
	tsPath := filepath.Join("..", "..", "web", "src", "schema.ts")
	raw, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("read web mirror %s: %v (run from internal/mapping)", tsPath, err)
	}
	for _, block := range []struct {
		name string
		keys []string
	}{
		{"TYPE_SPECS", objectKeys(string(raw), "TYPE_SPECS")},
		{"TYPE_META", objectKeys(string(raw), "TYPE_META")},
	} {
		if len(block.keys) != 33 {
			t.Errorf("web mirror %s holds %d keys, want 33: %v", block.name, len(block.keys), block.keys)
		}
		for _, k := range block.keys {
			if !specsSet[domain.ResourceType(k)] {
				t.Errorf("web mirror %s key %q is not a Specs type (mirror drift)", block.name, k)
			}
		}
		for rt := range specsSet {
			found := false
			for _, k := range block.keys {
				if k == string(rt) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("web mirror %s misses type %q (mirror drift)", block.name, rt)
			}
		}
	}
}

// TestMigrationRelationsCHECK pins the relation side of point (b): the
// relations CHECK of a freshly migrated database equals the RelationSpecs
// registry set — 29 values, with the W21 fiber_of correction in place of
// §4.2's carries_fiber.
func TestMigrationRelationsCHECK(t *testing.T) {
	_, relationCHECK := migratedCHECKLists(t)
	registry := map[domain.RelationType]bool{}
	for _, spec := range RelationSpecs() {
		registry[spec.Type] = true
	}
	if len(relationCHECK) != 29 {
		t.Errorf("relations CHECK lists %d values, want 29: %v", len(relationCHECK), relationCHECK)
	}
	for _, v := range relationCHECK {
		if !registry[domain.RelationType(v)] {
			t.Errorf("relations CHECK value %q has no RelationSpec (migration drift)", v)
		}
	}
	for rt := range registry {
		if !contains(relationCHECK, string(rt)) {
			t.Errorf("relations CHECK misses registry relation %q (migration drift)", rt)
		}
	}
}

// migratedCHECKLists builds a fresh database with the FULL migration
// registry (repo.Open applies every migration to v16+), closes it, then
// reads sqlite_master through a second read-only database/sql connection
// and returns the IN (…) literals of the resources.resource_type and
// relations.relation_type CHECK constraints.
func migratedCHECKLists(t *testing.T) (resourceCHECK, relationCHECK []string) {
	t.Helper()
	path := filepath.Join(testsupport.TempDir(t), "netops.db")
	db, err := repo.Open(path)
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated db: %v", err)
	}
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer ro.Close()
	resourceCHECK = checkInList(t, ro, "resources", "resource_type")
	relationCHECK = checkInList(t, ro, "relations", "relation_type")
	return resourceCHECK, relationCHECK
}

// checkInList parses the IN (…) list of one CHECK constraint out of
// sqlite_master: the CREATE statement of table is fetched, the
// "<column> IN (" occurrence located and every single-quoted literal of
// the list up to the matching ')' returned (order preserved).
func checkInList(t *testing.T, q *sql.DB, table, column string) []string {
	t.Helper()
	var sqlDDL string
	err := q.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&sqlDDL)
	if err != nil {
		t.Fatalf("%s: read DDL: %v", table, err)
	}
	marker := column + " IN ("
	i := strings.Index(sqlDDL, marker)
	if i < 0 {
		t.Fatalf("%s: no %q in DDL:\n%s", table, marker, sqlDDL)
	}
	rest := sqlDDL[i+len(marker):]
	depth := 1
	var literals []string
	var cur strings.Builder
	inQuote := false
	for _, r := range rest {
		switch {
		case r == '\'':
			inQuote = !inQuote
			if inQuote {
				cur.Reset()
			} else {
				literals = append(literals, cur.String())
			}
		case inQuote:
			cur.WriteRune(r)
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth == 0 {
				return literals
			}
		}
	}
	t.Fatalf("%s: unterminated IN list in DDL:\n%s", table, sqlDDL)
	return nil
}

// objectKeys extracts the top-level (2-space indent) keys of one exported
// object literal in TS source: the block from "export const <name>" to the
// next line that is exactly "};" — then every "<key>:" at two-space indent.
func objectKeys(src, name string) []string {
	startMark := "export const " + name
	i := strings.Index(src, startMark)
	if i < 0 {
		return nil
	}
	rest := src[i:]
	end := strings.Index(rest, "\n};")
	if end < 0 {
		return nil
	}
	block := rest[:end]
	var keys []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimRight(line, " ")
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue // only 2-space top-level keys
		}
		j := strings.Index(trimmed, ":")
		if j < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:j])
		if key == "" || !isTSIdent(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// isTSIdent reports whether s is a plain TS identifier (unquoted keys of
// the mirror objects are always lowercase snake_case).
func isTSIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// contains reports whether s is in the list.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// setDifference returns every domain type whose string form is NOT in the
// list (sorted for deterministic failure output).
func setDifference(set map[domain.ResourceType]bool, list []string) []string {
	var out []string
	for rt := range set {
		if !contains(list, string(rt)) {
			out = append(out, string(rt))
		}
	}
	sort.Strings(out)
	return out
}
