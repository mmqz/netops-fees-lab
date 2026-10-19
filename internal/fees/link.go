package fees

// link.go — the entity-graded fee-site ↔ resource matcher (#63, #58
// stage 3). Three grades:
//
//	exact  fee code == resource natural key, byte-for-byte → auto-confirmed
//	rule   equality under a NAMED normalization rule → auto-confirmed
//	       (the rule recorded per link — explainable by construction)
//	fuzzy  similarity ≥ threshold → PENDING proposals (up to 3), a human
//	       confirms or dismisses (the W005/W006 doctrine)
//
// The code universe comes from the analysis layer itself: the scan walks
// the live Parquet files (completed batches) and reads the site_code
// column via the PURE-GO parquet reader — column projection means only
// one string column is decoded; the default (non-duckdb) build can link
// too. A rolled-back batch's files are gone, so its codes drop out of
// the universe automatically.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/parquet-go/parquet-go"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// LinkableResourceTypes is the closed set of site-like resource types
// whose natural keys form the match universe (#63: site 局站ID /
// machine_room 机房ID / base_station 基站编码或名称兜底).
var LinkableResourceTypes = []domain.ResourceType{
	domain.ResourceTypeSite,
	domain.ResourceTypeMachineRoom,
	domain.ResourceTypeBaseStation,
}

// Link rules — the closed, explainable family (#63's 规则级). Each rule
// is a pure (feeCode, resourceKey) → bool predicate with a stable name
// recorded on the link for audit.
var linkRules = []struct {
	name  string
	match func(feeCode, resourceKey string) bool
}{
	{"fold", func(a, b string) bool { return foldCode(a) == foldCode(b) }},
	{"zero_pad", func(a, b string) bool { return stripLeadingZeros(a) == stripLeadingZeros(b) }},
}

// Fuzzy thresholds: similarity = 1 − dist/max(len); candidates at or
// above FUZZY_THRESHOLD become proposals, at most FUZZY_MAX_CANDIDATES
// per code (best first).
const (
	FUZZY_THRESHOLD      = 0.8
	FUZZY_MAX_CANDIDATES = 3
)

// foldCode normalizes a code for the fold rule: the full-width ASCII
// block (U+FF01–U+FF5E, ０-９ Ａ-Ｚ ａ-ｚ － ． （ …) to its half-width
// counterpart, whitespace collapsed away, case-folded — one uniform
// transform, no per-symbol special cases.
func foldCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			b.WriteRune(r - 0xFEE0)
		case r == 0x3000 || unicode.IsSpace(r):
			// drop (full-width space and every other whitespace)
		default:
			b.WriteRune(r)
		}
	}
	return strings.ToLower(b.String())
}

// stripLeadingZeros drops leading zeros when both remainder and original
// are all-digit (code systems disagree on zero padding; the rule is
// numeric-only to avoid mangling alphanumeric codes).
func stripLeadingZeros(s string) string {
	if s == "" || !allDigits(s) {
		return s
	}
	t := strings.TrimLeft(s, "0")
	if t == "" {
		return "0"
	}
	return t
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// levenshtein computes the edit distance (the fuzzy metric's basis).
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// Similarity normalizes the edit distance into [0,1].
func Similarity(a, b string) float64 {
	m := max(len([]rune(a)), len([]rune(b)))
	if m == 0 {
		return 1
	}
	return 1 - float64(levenshtein(a, b))/float64(m)
}

// resourceCandidate is one member of the match universe.
type resourceCandidate struct {
	id  string
	typ domain.ResourceType
	key string
}

// ScanResult reports one scan's outcome.
type ScanResult struct {
	Codes         int `json:"codes"`
	ExactLinks    int `json:"exactLinks"`
	RuleLinks     int `json:"ruleLinks"`
	FuzzyProposed int `json:"fuzzyProposed"`
	Unlinked      int `json:"unlinked"`
	HumanKept     int `json:"humanKept"`
}

// ScanLinks runs the graded matcher over the fee code universe (the live
// Parquet files) against the linkable resources. Deterministic: the code
// list is sorted; candidates sort by (similarity desc, key, id). All
// writes land in ONE transaction per code-batch — the scan is idempotent
// (re-scan replaces auto links, never touches human ones).
func (s *Service) ScanLinks(ctx context.Context) (ScanResult, error) {
	live, err := s.db.FeeBatches.ListParquetLiveBatchIDs(ctx)
	if err != nil {
		return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
	}
	codes, err := distinctSiteCodes(s.analysisDir(), live)
	if err != nil {
		return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
	}

	candidates := make([]resourceCandidate, 0, 1024)
	for _, typ := range LinkableResourceTypes {
		resources, err := s.db.Resources.ListByType(ctx, typ)
		if err != nil {
			return ScanResult{}, fmt.Errorf("fees: link scan (%s): %w", typ, err)
		}
		for _, res := range resources {
			candidates = append(candidates, resourceCandidate{id: res.ID, typ: res.Type, key: res.NaturalKey})
		}
	}

	// Dropout sweep: auto links (confirmed or pending) whose code left
	// the live universe follow the data — the scan is their authority.
	// Human-confirmed links and dismissed proposals are DECISIONS, they
	// outlive the data (the W006 doctrine).
	if err := s.sweepAutoDropouts(ctx, codes); err != nil {
		return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
	}

	var result ScanResult
	result.Codes = len(codes)
	now := s.clock()
	for _, code := range codes {
		// exact
		if c, ok := findExact(candidates, code); ok {
			if err := s.upsertAuto(ctx, code, c, domain.LinkGradeExact, "", 0, now); err != nil {
				return ScanResult{}, err
			}
			result.ExactLinks++
			continue
		}
		// rules (deterministic order; first hit wins and is recorded)
		matched := false
		for _, rule := range linkRules {
			if c, ok := findByRule(candidates, code, rule.match); ok {
				if err := s.upsertAuto(ctx, code, c, domain.LinkGradeRule, rule.name, 0, now); err != nil {
					return ScanResult{}, err
				}
				result.RuleLinks++
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// fuzzy proposals (idempotent: a pending or dismissed pair is
		// never re-proposed — no duplicates, no resurrection of human
		// rejections). Unlinked counts codes with NO candidates at all.
		proposals := topFuzzy(candidates, code)
		if len(proposals) == 0 {
			result.Unlinked++
			continue
		}
		fresh := 0
		for _, p := range proposals {
			decided, err := s.db.FeeSiteLinks.PairDecided(ctx, code, p.c.id)
			if err != nil {
				return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
			}
			if decided {
				continue
			}
			fresh++
			if err := s.db.Do(ctx, func(tx *repo.Tx) error {
				return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
					ID: domain.NewID("fsl"), FeeSiteCode: code,
					ResourceID: p.c.id, ResourceType: p.c.typ, ResourceKey: p.c.key,
					Grade: domain.LinkGradeFuzzy, Similarity: p.similarity,
					Status: domain.LinkStatusPending, Source: domain.LinkSourceAuto,
					CreatedAt: now,
				})
			}); err != nil {
				return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
			}
		}
		result.FuzzyProposed += fresh
	}
	// human-kept count (the scan never overwrote these)
	links, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusConfirmed})
	if err == nil {
		for _, l := range links {
			if l.Source == domain.LinkSourceHuman {
				result.HumanKept++
			}
		}
	}
	return result, nil
}

// sweepAutoDropouts deletes auto (non-dismissed) links whose code is not
// in the current universe. The NOT IN set is chunked (SQLite parameter
// limits); an empty universe sweeps everything auto.
func (s *Service) sweepAutoDropouts(ctx context.Context, codes []string) error {
	liveSet := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		liveSet[c] = struct{}{}
	}
	links, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{})
	if err != nil {
		return err
	}
	var doomed []string
	for _, l := range links {
		if l.Source != domain.LinkSourceAuto || l.Status == domain.LinkStatusDismissed {
			continue
		}
		if _, live := liveSet[l.FeeSiteCode]; !live {
			doomed = append(doomed, l.ID)
		}
	}
	if len(doomed) == 0 {
		return nil
	}
	return s.db.Do(ctx, func(tx *repo.Tx) error {
		for _, id := range doomed {
			if _, err := tx.FeeSiteLinks.DeleteByID(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) upsertAuto(ctx context.Context, code string, c resourceCandidate, grade domain.LinkGrade, rule string, sim float64, now time.Time) error {
	return s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.UpsertAutoLink(ctx, domain.FeeSiteLink{
			ID: domain.NewID("fsl"), FeeSiteCode: code,
			ResourceID: c.id, ResourceType: c.typ, ResourceKey: c.key,
			Grade: grade, Rule: rule, Similarity: sim,
			Status: domain.LinkStatusConfirmed, Source: domain.LinkSourceAuto,
			CreatedAt: now,
		})
	})
}

func findExact(candidates []resourceCandidate, code string) (resourceCandidate, bool) {
	for _, c := range candidates {
		if c.key == code {
			return c, true
		}
	}
	return resourceCandidate{}, false
}

func findByRule(candidates []resourceCandidate, code string, match func(a, b string) bool) (resourceCandidate, bool) {
	for _, c := range candidates {
		if match(code, c.key) {
			return c, true
		}
	}
	return resourceCandidate{}, false
}

// fuzzyCandidate is one similarity-scored proposal.
type fuzzyCandidate struct {
	c          resourceCandidate
	similarity float64
}

// topFuzzy returns up to FUZZY_MAX_CANDIDATES candidates at or above the
// threshold, best first, ties broken deterministically (key then id).
func topFuzzy(candidates []resourceCandidate, code string) []fuzzyCandidate {
	var scored []fuzzyCandidate
	for _, c := range candidates {
		if c.key == "" || code == "" {
			continue
		}
		sim := Similarity(code, c.key)
		if sim >= FUZZY_THRESHOLD {
			scored = append(scored, fuzzyCandidate{c: c, similarity: sim})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].similarity != scored[j].similarity {
			return scored[i].similarity > scored[j].similarity
		}
		if scored[i].c.key != scored[j].c.key {
			return scored[i].c.key < scored[j].c.key
		}
		return scored[i].c.id < scored[j].c.id
	})
	if len(scored) > FUZZY_MAX_CANDIDATES {
		scored = scored[:FUZZY_MAX_CANDIDATES]
	}
	return scored
}

// distinctSiteCodes reads the distinct site_code values of the LIVE
// Parquet files (the keep-set = completed batch ids) — pure Go, column
// projection (only site_code is decoded). Deterministic order: sorted.
func distinctSiteCodes(analysisDir string, live map[string]struct{}) ([]string, error) {
	root := filepath.Join(analysisDir, "fees")
	seen := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil //nolint — unreadable trees are skipped, the scan is best-effort per file
		}
		name := d.Name()
		if !strings.HasPrefix(name, "batch-") || !strings.HasSuffix(name, ".parquet") {
			return nil
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "batch-"), ".parquet")
		if _, keep := live[id]; !keep {
			return nil // an orphan or non-completed batch — not in the universe
		}
		f, err := os.Open(path)
		if err != nil {
			return nil //nolint — one unreadable file never aborts the scan
		}
		r := parquet.NewGenericReader[feeParquetRow](f)
		rows := make([]feeParquetRow, 256)
		for {
			n, err := r.Read(rows)
			for i := 0; i < n; i++ {
				if rows[i].SiteCode != "" {
					seen[rows[i].SiteCode] = struct{}{}
				}
			}
			if err != nil {
				break
			}
		}
		_ = r.Close()
		_ = f.Close()
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)
	return out, nil
}
