package fees

// link.go — the entity-graded fee-site ↔ resource matcher (#63, #58
// stage 3). Three grades:
//
//      exact  fee code == resource natural key, byte-for-byte → auto-confirmed
//      rule   equality under a NAMED normalization rule → auto-confirmed
//             (the rule recorded per link — explainable by construction)
//      fuzzy  similarity ≥ threshold → PENDING proposals (up to 3), a human
//             confirms or dismisses (the W005/W006 doctrine)
//
// The code universe comes from the analysis layer itself: the scan walks
// the live Parquet files (completed batches) and reads the site_code
// column via the PURE-GO parquet reader — column projection means only
// one string column is decoded; the default (non-duckdb) build can link
// too. A rolled-back batch's files are gone, so its codes drop out of
// the universe automatically.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/parquet-go/parquet-go"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// LinkableResourceTypes is the closed set of site-like resource types
// whose natural keys form the match universe (#63: site 局站ID /
// machine_room 机房ID / base_station 基站编码或名称兜底).
var LinkableResourceTypes = []domain.ResourceType{
	domain.ResourceTypeSite,
	domain.ResourceTypeMachineRoom,
	domain.ResourceTypeBaseStation,
}

// Link rules — the closed, explainable family (#63's 规则级). Every
// rule is a pure key TRANSFORM (same function applied to both sides;
// equality of the transforms = match) so the scan can index the
// candidates per rule instead of scanning them per code (#65 P1-b). The
// transform is recorded by name on the link for audit.
var linkRules = []struct {
	name string
	key  func(s string) string
}{
	{"fold", foldCode},
	{"zero_pad", stripLeadingZeros},
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

// similarityAtLeast is the scan's decision-exact fast path (#65 P1-b):
// it returns (sim, true) — with the SAME similarity value the naive
// Similarity would produce — only when the similarity reaches threshold,
// and (0, false) otherwise, without computing distances that provably
// cannot reach the threshold. Three lossless cuts, in firing order:
//
//  1. length pre-filter: dist >= |lenDiff|, and Similarity is
//     non-increasing in dist under Go's float semantics (both the
//     division and the subtraction round monotonically), so the pair is
//     below threshold already at its MINIMUM possible distance when
//     1 − |lenDiff|/maxLen < threshold — decided by evaluating the naive
//     expression itself at dist = lenDiff;
//  2. banded DP: cells with |i−j| > cap have dist >= |i−j| > cap, so the
//     computation stays inside a 2*cap+1 diagonal band (out-of-band
//     cells clamp to cap+1 — every path through them stays above cap);
//  3. early abort: every entry of DP row i+1 is >= some entry of row i
//     (the recurrence only adds non-negative costs), so the band's row
//     minimum is non-decreasing — once it exceeds cap the final distance
//     provably does too.
//
// cap itself is derived by evaluating the naive threshold expression
// (monotone in dist), never by float-truncating the algebra, so the fast
// path's accept/reject decisions are bit-identical to the naive path's.
//
// The string form converts once and delegates to the rune form — the
// scan's hot loop (topFuzzy) calls the rune form directly with
// PRE-CONVERTED slices (candidate keys converted once per scan at
// candidate-build time, the fee code once per code): 10k codes × 1k
// candidates otherwise paid 20M []rune conversions, each an allocation +
// copy — the single biggest cost of the sweep after the banded DP
// itself (#76 follow-up: the acceptance budget must hold on a loaded
// 2-core box, so the headroom matters as much as the algorithm).
func similarityAtLeast(a, b string, threshold float64) (float64, bool) {
	return similarityAtLeastRune([]rune(a), []rune(b), threshold)
}

// similarityAtLeastRune is similarityAtLeast over pre-converted rune
// slices. Same decisions, same values, zero conversions.
func similarityAtLeastRune(ar, br []rune, threshold float64) (float64, bool) {
	m := max(len(ar), len(br))
	if m == 0 {
		return 1, true // the Similarity convention: two empty keys match fully
	}
	lenDiff := len(ar) - len(br)
	if lenDiff < 0 {
		lenDiff = -lenDiff
	}
	// cap: the largest integer distance whose naive Similarity still
	// reaches the threshold (start from the algebraic estimate, then
	// correct ±1 by the naive expression itself).
	capDist := int((1 - threshold) * float64(m))
	if capDist < 0 {
		capDist = 0
	}
	if capDist < m && 1-float64(capDist+1)/float64(m) >= threshold {
		capDist++
	}
	for capDist > 0 && 1-float64(capDist)/float64(m) < threshold {
		capDist--
	}
	// length pre-filter: the minimum possible distance already fails.
	if lenDiff > capDist {
		return 0, false
	}
	// character-count cut: one edit changes a character-count vector by at
	// most 2 (a substitution −1/+1, an insertion +1, a deletion −1), so
	// Σ|Δcount| <= 2·dist — a composition difference above 2·capDist
	// cannot reach the threshold. ASCII-only counter; any non-ASCII rune
	// skips the cut (Chinese name keys are normally eliminated by the
	// length cut already). Lossless for the decision, cheap for the bulk:
	// it kills composition-far pairs at ~O(len) instead of the banded DP.
	if delta, ascii := asciiCountDelta(ar, br); ascii && delta > 2*int32(capDist) {
		return 0, false
	}
	dist, ok := levenshteinBanded(ar, br, capDist)
	if !ok || dist > capDist {
		return 0, false
	}
	return 1 - float64(dist)/float64(m), true
}

// levenshteinBanded computes the edit distance inside the |i−j| <= cap
// band and reports (dist, true) when the distance is <= cap; (0, false)
// when it provably exceeds cap (early abort or band-edge dominance).
// Out-of-band cells clamp to cap+1: a clamped cell sits above cap, and
// every path through it accumulates only non-negative costs, so it can
// never contribute to a sub-cap result — a final dist <= cap is always
// the genuine distance. Per row only the band plus its two guard cells
// are written: the next row reads exactly that window (bands shift by
// one per row), and rows further away never touch it, so the two reused
// buffers cannot leak stale values into a decision.
func levenshteinBanded(a, b []rune, cap int) (int, bool) {
	n, m := len(a), len(b)
	if n-m > cap || m-n > cap {
		return 0, false
	}
	inf := cap + 1 // the clamp: anything above cap is equivalent for the decision
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := 0; j <= m; j++ { // row 0: fully initialized — the base every later row reads into
		if j <= cap {
			prev[j] = j
		} else {
			prev[j] = inf
		}
	}
	for i := 1; i <= n; i++ {
		lo, hi := i-cap, i+cap
		if lo < 1 {
			lo = 1
		}
		if hi > m {
			hi = m
		}
		// reset only this row's write window: [lo-1, hi+1] clamped to [0, m]
		wlo, whi := lo-1, hi+1
		if wlo < 0 {
			wlo = 0
		}
		if whi > m {
			whi = m
		}
		for j := wlo; j <= whi; j++ {
			cur[j] = inf
		}
		if i <= cap {
			cur[0] = i // the in-band row base d[i][0]
		}
		rowMin := inf
		for j := lo; j <= hi; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := prev[j] + 1                  // deletion
			if t := prev[j-1] + cost; t < v { // substitution
				v = t
			}
			if t := cur[j-1] + 1; t < v { // insertion
				v = t
			}
			if v > inf {
				v = inf
			}
			cur[j] = v
			if v < rowMin {
				rowMin = v
			}
		}
		if rowMin > cap {
			return 0, false // non-decreasing row minimum — distance provably > cap
		}
		prev, cur = cur, prev
	}
	d := prev[m]
	if d > cap {
		return 0, false
	}
	return d, true
}

// asciiCountDelta computes Σ_c |count_a(c) − count_b(c)| over the two
// rune slices (interpreted as code points); ok=false when either side
// carries a rune above U+007F (the cut is skipped for non-ASCII keys).
func asciiCountDelta(a, b []rune) (int32, bool) {
	var buf [128]int32
	for _, r := range a {
		if r < 0 || r >= 128 {
			return 0, false
		}
		buf[r]++
	}
	for _, r := range b {
		if r < 0 || r >= 128 {
			return 0, false
		}
		buf[r]--
	}
	var delta int32
	for _, v := range buf {
		if v > 0 {
			delta += v
		} else {
			delta -= v
		}
	}
	return delta, true
}

// resourceCandidate is one member of the match universe.
type resourceCandidate struct {
	id       string
	typ      domain.ResourceType
	key      string
	keyRunes []rune // pre-converted once at candidate-build time (#76 follow-up): the fuzzy sweep reuses it for every code
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

// scanUniverse is the per-scan candidate index (#65 P1-b): the rule-grade
// lookups run against hash maps built ONCE per scan instead of linear
// scans per code (10k codes x 1.4k candidates used to mean 14M+ string
// comparisons before the fuzzy step even started). First-wins insertion
// preserves the deterministic candidate order the linear scan used (a
// later duplicate key never displaces the first).
type scanUniverse struct {
	candidates []resourceCandidate
	ruleKeys   []map[string]int // per linkRules[i]: transformed key -> first candidate index
}

// buildScanUniverse indexes the candidates for the rule-grade lookups.
func buildScanUniverse(candidates []resourceCandidate) scanUniverse {
	u := scanUniverse{
		candidates: candidates,
		ruleKeys:   make([]map[string]int, len(linkRules)),
	}
	for i := range u.ruleKeys {
		u.ruleKeys[i] = make(map[string]int, len(candidates))
	}
	for idx, c := range candidates {
		for ri, rule := range linkRules {
			k := rule.key(c.key)
			if _, dup := u.ruleKeys[ri][k]; !dup {
				u.ruleKeys[ri][k] = idx // first wins — deterministic, as the linear scan was
			}
		}
	}
	return u
}

// byRule looks the code up under rule ri's transform; the first candidate
// whose transform equals the code's transform wins (deterministic).
func (u scanUniverse) byRule(ri int, code string) (resourceCandidate, bool) {
	if idx, ok := u.ruleKeys[ri][linkRules[ri].key(code)]; ok {
		return u.candidates[idx], true
	}
	return resourceCandidate{}, false
}

// codeVerdict is one fee code's decision: the auto-confirmed link
// (exact/rule), the fresh fuzzy proposals (already filtered by
// PairDecided), or the unlinked flag (zero candidates at all). Deciding
// and writing are separated so the decide phase can run in parallel
// while the write phase stays a deterministic ordered replay.
type codeVerdict struct {
	auto      *domain.FeeSiteLink
	proposals []domain.FeeSiteLink
	unlinked  bool
}

// decideCode computes one code's verdict. Pure decision + one indexed
// read per fuzzy candidate — safe for concurrent use from the decide
// worker pool.
func (s *Service) decideCode(ctx context.Context, universe scanUniverse,
	candidates []resourceCandidate, code string, now time.Time) (codeVerdict, error) {
	// exact (byte-for-byte natural-key equality)
	if c, ok := findExact(candidates, code); ok {
		return codeVerdict{auto: &domain.FeeSiteLink{
			ID: domain.NewID("fsl"), FeeSiteCode: code,
			ResourceID: c.id, ResourceType: c.typ, ResourceKey: c.key,
			Grade:  domain.LinkGradeExact,
			Status: domain.LinkStatusConfirmed, Source: domain.LinkSourceAuto,
			CreatedAt: now,
		}}, nil
	}
	// rules (deterministic order; first hit wins and is recorded)
	for ri, rule := range linkRules {
		if c, ok := universe.byRule(ri, code); ok {
			return codeVerdict{auto: &domain.FeeSiteLink{
				ID: domain.NewID("fsl"), FeeSiteCode: code,
				ResourceID: c.id, ResourceType: c.typ, ResourceKey: c.key,
				Grade: domain.LinkGradeRule, Rule: rule.name,
				Status: domain.LinkStatusConfirmed, Source: domain.LinkSourceAuto,
				CreatedAt: now,
			}}, nil
		}
	}
	// fuzzy proposals (idempotent: a pending or dismissed pair is never
	// re-proposed — no duplicates, no resurrection of human rejections).
	// Unlinked counts codes with NO candidates at all.
	proposals := topFuzzy(candidates, code)
	if len(proposals) == 0 {
		return codeVerdict{unlinked: true}, nil
	}
	var fresh []domain.FeeSiteLink
	for _, p := range proposals {
		decided, err := s.db.FeeSiteLinks.PairDecided(ctx, code, p.c.id)
		if err != nil {
			return codeVerdict{}, fmt.Errorf("fees: link scan: %w", err)
		}
		if decided {
			continue
		}
		fresh = append(fresh, domain.FeeSiteLink{
			ID: domain.NewID("fsl"), FeeSiteCode: code,
			ResourceID: p.c.id, ResourceType: p.c.typ, ResourceKey: p.c.key,
			Grade: domain.LinkGradeFuzzy, Similarity: p.similarity,
			Status: domain.LinkStatusPending, Source: domain.LinkSourceAuto,
			CreatedAt: now,
		})
	}
	return codeVerdict{proposals: fresh}, nil
}

// ScanLinks runs the graded matcher over the fee code universe (the live
// Parquet files) against the linkable resources. Deterministic: the code
// list is sorted; candidates sort by (similarity desc, key, id). The
// scan's writes land in CHUNKED transactions of 500 (#65 P1-b: one db.Do
// per write meant 10k+ single-statement transactions on a 10k-code
// universe; the chunk keeps all-or-nothing semantics per chunk and the
// scan stays idempotent — the SQL sweep + per-code upserts make a
// partially-committed interrupted re-scan converge).
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
			candidates = append(candidates, resourceCandidate{
				id: res.ID, typ: res.Type, key: res.NaturalKey,
				keyRunes: []rune(res.NaturalKey), // one conversion per candidate, reused by every code's sweep
			})
		}
	}
	universe := buildScanUniverse(candidates)

	// Dropout sweep, pushed down to SQL (#65 P1-b: the sweep used to load
	// the whole link table and filter in memory): auto links (confirmed
	// or pending) whose code left the live universe follow the data —
	// the scan is their authority. Human-confirmed links and dismissed
	// proposals are DECISIONS, they outlive the data (the W006 doctrine).
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		_, err := tx.FeeSiteLinks.DeleteAutoNotIn(ctx, codes)
		return err
	}); err != nil {
		return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
	}

	var result ScanResult
	result.Codes = len(codes)
	now := s.clock()

	// Decide phase, PARALLEL over codes (#65 P1-b): the fuzzy step is pure
	// CPU (the pre-filter + banded DP), so the worker pool is the scan's
	// last multiplier on a 10k-code universe — the HTTP endpoint stays
	// responsive instead of timing out on a single core. Determinism is
	// untouched: verdicts land in a per-code slice by index, and the write
	// phase replays them in the sorted code order exactly as before. The
	// only concurrent I/O is PairDecided — a pool read against the
	// indexed (fee_site_code, resource_id) lookups, WAL-safe by
	// construction.
	verdicts := make([]codeVerdict, len(codes))
	{
		var (
			wg       sync.WaitGroup
			next     atomic.Int32
			errMu    sync.Mutex
			firstErr error
		)
		workers := runtime.GOMAXPROCS(0)
		if workers > len(codes) {
			workers = len(codes)
		}
		if workers < 1 {
			workers = 1
		}
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1)) - 1
					if i >= len(codes) {
						return
					}
					v, err := s.decideCode(ctx, universe, candidates, codes[i], now)
					if err != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						errMu.Unlock()
						continue
					}
					verdicts[i] = v
				}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			return ScanResult{}, fmt.Errorf("fees: link scan: %w", firstErr)
		}
	}

	// Write phase: replay the verdicts in code order, chunked
	// transactions of 500 (all-or-nothing per chunk; idempotent re-scan
	// converges after an interrupted chunk).
	var writes []func(tx *repo.Tx) error
	for _, v := range verdicts {
		switch {
		case v.auto != nil:
			link := *v.auto
			writes = append(writes, func(tx *repo.Tx) error {
				return tx.FeeSiteLinks.UpsertAutoLink(ctx, link)
			})
			if v.auto.Grade == domain.LinkGradeExact {
				result.ExactLinks++
			} else {
				result.RuleLinks++
			}
		case v.unlinked:
			result.Unlinked++
		default:
			for _, link := range v.proposals {
				link := link
				writes = append(writes, func(tx *repo.Tx) error {
					return tx.FeeSiteLinks.InsertProposal(ctx, link)
				})
			}
			result.FuzzyProposed += len(v.proposals)
		}
	}

	// write in chunks — all-or-nothing per chunk.
	const chunk = 500
	for lo := 0; lo < len(writes); lo += chunk {
		hi := lo + chunk
		if hi > len(writes) {
			hi = len(writes)
		}
		if err := s.db.Do(ctx, func(tx *repo.Tx) error {
			for _, w := range writes[lo:hi] {
				if err := w(tx); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return ScanResult{}, fmt.Errorf("fees: link scan: %w", err)
		}
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

// findExact returns the first candidate whose natural key equals the fee
// code byte-for-byte (the exact grade). Kept as the linear scan: the
// candidates are the site-like resources (three types, each bounded by
// the resource universe), and exact equality against a hash map would
// save little at realistic sizes while the rule index carries the real
// win (#65 P1-b).
func findExact(candidates []resourceCandidate, code string) (resourceCandidate, bool) {
	for _, c := range candidates {
		if c.key == code {
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
// similarityAtLeast is the decision-exact fast path (#65 P1-b): the
// length pre-filter skips most of the 10k-codes x 10k-candidates Levenshtein
// work before any DP runs, with accept/reject decisions bit-identical to
// the naive Similarity >= threshold comparison.
func topFuzzy(candidates []resourceCandidate, code string) []fuzzyCandidate {
	var scored []fuzzyCandidate
	codeRunes := []rune(code) // one conversion per code, shared by all its candidates
	for _, c := range candidates {
		if c.key == "" || code == "" {
			continue
		}
		kr := c.keyRunes
		if kr == nil {
			kr = []rune(c.key) // defensive: candidates built outside the scan path convert lazily
		}
		if sim, ok := similarityAtLeastRune(codeRunes, kr, FUZZY_THRESHOLD); ok {
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

// readBatchSiteCodes streams one live Parquet file's site_code column into
// seen. It runs guarded: parquet-go's NewGenericReader PANICS on
// structurally corrupt files (openFile → invalid magic/footer → reader.go:39
// `panic(err)`), and a corrupted batch on disk is a DATA error, not a
// programmer error — it must surface as this scan's fail-loud error, never
// as a process crash (#84).
func readBatchSiteCodes(path string, seen map[string]struct{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("fees: 读取 %s: 损坏的 Parquet 文件: %v", path, r)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("fees: 打开 %s: %w", path, err)
	}
	// The file handle closes BEFORE the panicking constructor ever runs:
	// parquet-go's NewGenericReader panics on corrupt magic/footer, and a
	// defer registered after it would never fire — the recovered error
	// returned while the handle stayed open. On Windows that leak pins
	// the parquet file until process exit (t.TempDir cleanup then fails
	// with "being used by another process", #124's environment failure);
	// on POSIX it burns an fd per corrupt-file scan.
	defer func() { _ = f.Close() }()
	r := parquet.NewGenericReader[feeParquetRow](f)
	rows := make([]feeParquetRow, 256)
	defer func() {
		_ = r.Close()
	}()
	for {
		n, rerr := r.Read(rows)
		for i := 0; i < n; i++ {
			if rows[i].SiteCode != "" {
				seen[rows[i].SiteCode] = struct{}{}
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				return fmt.Errorf("fees: 读取 %s: %w", path, rerr)
			}
			return nil
		}
	}
}

// distinctSiteCodes reads the distinct site_code values of the LIVE
// Parquet files (the keep-set = completed batch ids) — pure Go, column
// projection (only site_code is decoded). Deterministic order: sorted.
//
// #84: this is the UNIVERSE that feeds DeleteAutoNotIn — "auto links
// follow the data". A read error shrunk silently to a partial universe
// would delete live auto links on a transient FS hiccup (the empty
// universe wipes them all). So read errors are NOT best-effort here:
// any walk/open/read failure on the live batch set aborts the scan, and
// only the "no fees directory at all AND no live batches" state counts
// as a legitimately empty universe.
func distinctSiteCodes(analysisDir string, live map[string]struct{}) ([]string, error) {
	root := filepath.Join(analysisDir, "fees")
	seen := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Missing root with no live batches is the one legal empty
			// universe (fresh install; auto links follow data that never
			// existed). Everything else — a vanished root under live
			// batches, permission errors, I/O faults — must fail loud.
			if errors.Is(walkErr, fs.ErrNotExist) && len(live) == 0 && path == root {
				return nil
			}
			return fmt.Errorf("fees: 扫描 %s: %w", path, walkErr)
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "batch-") || !strings.HasSuffix(name, ".parquet") {
			return nil
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "batch-"), ".parquet")
		if _, keep := live[id]; !keep {
			return nil // an orphan or non-completed batch — not in the universe
		}
		return readBatchSiteCodes(path, seen)
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
