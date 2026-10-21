package app

// usage_closset_test.go — issue #108's drift gates for every CLI usage
// surface that names subcommands or resource types.
//
// Two walls, both CLOSED:
//
//   - SUBCOMMAND wall: a dispatch switch cannot be reflected at runtime,
//     so these tests go/ast-parse the dispatcher source and compare the
//     switch's case set with what the usage messages actually claim —
//     bidirectionally. A new switch case that forgets its slice entry, or
//     a message claiming a subcommand that does not dispatch, both fail.
//     #108 shipped exactly that class of drift twice: the top-level usage
//     forgot `backup`, and both fees strings forgot `evidence`/`risks`.
//
//   - TYPE wall: the --type validation errors must list the FULL registry
//     set (derived from domain.AllResourceTypes — the pre-#108 surfaces
//     hand-copied the pre-W21 8 types and went stale at 33), and the flag
//     help must POINT AT that error instead of enumerating types itself
//     (the #91 MCP-face doctrine: a description that lists nothing cannot
//     drift per extension).

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestTopLevelUsageListsEveryDispatchedSubcommand pins both top-level usage
// surfaces (missing subcommand / unknown subcommand) against the CLI
// dispatch switch, both directions: every dispatched case must be listed,
// and every listed name must dispatch (#108: `backup` dispatched but was
// missing from the no-args message for its whole life until this gate).
func TestTopLevelUsageListsEveryDispatchedSubcommand(t *testing.T) {
	want := switchCases(t, "cli.go", "CLI")

	var stdout, stderr bytes.Buffer
	if code := CLI(nil, &stdout, &stderr); code != exitUsage {
		t.Fatalf("CLI() with no args exit = %d, want %d (usage)", code, exitUsage)
	}
	assertSameSet(t, "no-subcommand usage", want, claimedSubcommands(cliErrorMessage(t, &stdout)))

	stdout.Reset()
	stderr.Reset()
	if code := CLI([]string{"__no_such_subcommand__"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("CLI() with an unknown subcommand exit = %d, want %d (usage)", code, exitUsage)
	}
	assertSameSet(t, "unknown-subcommand usage", want, claimedSubcommands(cliErrorMessage(t, &stdout)))
}

// TestDispatcherUsageListsEveryDispatchedSubcommand runs the same closed-set
// pin over every nested dispatcher: the two usage messages each dispatcher
// can produce (missing sub / unknown sub) must carry exactly the switch's
// case set. The splitSubArgs-style parents (query/changes/renames/fees)
// open the application BEFORE dispatching, so the unknown-sub call rides a
// throwaway --db to reach the default branch.
func TestDispatcherUsageListsEveryDispatchedSubcommand(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "netops.db")
	// #108 found both fees strings stale (`evidence`/`risks` missing), so
	// fees is the regression headliner; the rest are guarded by the same gate.
	dispatchers := []struct {
		name        string
		file        string
		fnName      string
		fn          func(args []string, stdout, stderr io.Writer) error
		noSubArgs   []string
		unknownArgs []string
	}{
		{"query", "cli.go", "cmdQuery", cmdQuery, nil, []string{"--db", db, "__nope__"}},
		{"changes", "cli.go", "cmdChanges", cmdChanges, nil, []string{"--db", db, "__nope__"}},
		{"renames", "cli.go", "cmdRenames", cmdRenames, nil, []string{"--db", db, "__nope__"}},
		{"lab", "cli.go", "cmdLab", cmdLab, nil, []string{"__nope__"}},
		{"templates", "cli.go", "cmdTemplates", cmdTemplates, nil, []string{"__nope__"}},
		{"fees", "fees_cli.go", "cmdFees", cmdFees, nil, []string{"--db", db, "--data", dir, "__nope__"}},
	}
	for _, d := range dispatchers {
		t.Run(d.name, func(t *testing.T) {
			want := switchCases(t, d.file, d.fnName)

			err := d.fn(d.noSubArgs, &bytes.Buffer{}, &bytes.Buffer{})
			assertSameSet(t, d.name+" missing-subcommand usage", want,
				claimedSubcommands(usageErrorMessage(t, err)))

			err = d.fn(d.unknownArgs, &bytes.Buffer{}, &bytes.Buffer{})
			assertSameSet(t, d.name+" unknown-subcommand usage", want,
				claimedSubcommands(usageErrorMessage(t, err)))
		})
	}
}

// TestCLITypeErrorsListRegistryClosedSet pins the three --type validation
// errors to the domain registry, bidirectionally: every registry type must
// appear in the error, and nothing else may. The help texts point at these
// errors for the full closed set (see TestTypeFlagHelpsPointAtRegistry),
// so the errors not listing the set would make the whole doctrine hollow.
func TestCLITypeErrorsListRegistryClosedSet(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "netops.db")
	// 33-pin, four-point style: extending the registry must fail here until
	// the count is consciously revisited (mapping's four-point gate holds
	// the registry itself to the same number).
	all := domain.AllResourceTypes()
	if len(all) != 33 {
		t.Fatalf("domain registry holds %d resource types, want 33 — if the closed set changed, update this pin (and the four-point gate in internal/mapping)", len(all))
	}
	want := make([]string, 0, len(all))
	for _, rt := range all {
		want = append(want, rt.String())
	}

	cases := []struct {
		name string
		args []string
	}{
		{"query resources", []string{"query", "--db", db, "resources", "--type", "__bogus__"}},
		{"query nearest", []string{"query", "--db", db, "nearest", "--type", "__bogus__"}},
		{"renames list", []string{"renames", "--db", db, "list", "--type", "__bogus__"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := CLI(tc.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit = %d, want %d (usage); stdout: %s", code, exitUsage, stdout.String())
			}
			msg := cliErrorMessage(t, &stdout)
			got := typesClaimedInError(t, msg)
			if len(got) == 0 {
				t.Fatalf("--type error does not list the closed set: %q", msg)
			}
			g := slices.Clone(got)
			slices.Sort(g)
			w := slices.Clone(want)
			slices.Sort(w)
			if !slices.Equal(w, g) {
				t.Errorf("--type error lists %d types, registry holds %d (symmetric diff would fail the HTTP twin of this gate)", len(got), len(want))
			}
			// The two halves of the old lie: a pre-W21 type and a W21 type
			// must BOTH be there — the pre-#108 messages froze at the 8.
			for _, pin := range []string{"ptp_port", "pon_port"} {
				if !slices.Contains(got, pin) {
					t.Errorf("--type error omits registry type %q: %q", pin, msg)
				}
			}
		})
	}
}

// TestTypeFlagHelpsPointAtRegistryClosedSet pins the flag help texts to the
// #91 doctrine: no hand-copied type enumeration (they froze at the pre-W21
// 8 types and lied per extension), just "registered type" + a pointer at
// the validation error that lists the full derived closed set.
func TestTypeFlagHelpsPointAtRegistryClosedSet(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "netops.db")
	// The exact stale signature the pre-#108 help texts carried — kept
	// verbatim so a revert of the doctrine fails loudly here.
	const staleList = "device / ip_pool / circuit / port / base_station / site / machine_room / ptp_port"
	const staleCommaList = "device, ip_pool, circuit, port, base_station, site, machine_room, ptp_port"
	cases := []struct {
		name     string
		args     []string
		doctrine []string
		stale    []string
	}{
		{
			name:     "export --types",
			args:     []string{"export", "--definitely-not-a-flag"},
			doctrine: []string{"缺省导出全部注册类型", "非法值报错并列出注册闭集"},
			stale:    []string{staleCommaList},
		},
		{
			name:     "query resources --type",
			args:     []string{"query", "--db", db, "resources", "--definitely-not-a-flag"},
			doctrine: []string{"注册资源类型之一", "完整闭集在 --type 校验错误信息里自动列出"},
			stale:    []string{staleList},
		},
		{
			name:     "query nearest --type",
			args:     []string{"query", "--db", db, "nearest", "--definitely-not-a-flag"},
			doctrine: []string{"注册类型之一", "完整闭集在 --type 校验错误信息里自动列出"},
			stale:    []string{staleList},
		},
		{
			name:     "renames list --type",
			args:     []string{"renames", "--db", db, "list", "--definitely-not-a-flag"},
			doctrine: []string{"注册资源类型之一", "完整闭集在 --type 校验错误信息里自动列出"},
			stale:    []string{staleList},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := CLI(tc.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit = %d, want %d (usage); stdout: %s stderr: %s", code, exitUsage, stdout.String(), stderr.String())
			}
			usage := stderr.String()
			for _, d := range tc.doctrine {
				if !strings.Contains(usage, d) {
					t.Errorf("flag help lacks the #91-doctrine phrase %q:\n%s", d, usage)
				}
			}
			for _, s := range tc.stale {
				if strings.Contains(usage, s) {
					t.Errorf("flag help still hand-copies the stale pre-W21 type list:\n%s", usage)
				}
			}
		})
	}
}

// TestFeesImportHelpNoLongerClaimsXLS pins #108-C: the fees import help must
// match the #92 truth — .xls is REFUSED (Excel 2003 binary; the reader and
// the snapshot layer both reject it with a dedicated message), so the help
// claiming ".csv / .xlsx / .xls" was a lie the operator discovered only at
// import time.
func TestFeesImportHelpNoLongerClaimsXLS(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "netops.db")
	var stdout, stderr bytes.Buffer
	code := CLI([]string{"fees", "--db", db, "--data", dir, "import", "--definitely-not-a-flag"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (usage); stdout: %s", code, exitUsage, stdout.String())
	}
	usage := stderr.String()
	if strings.Contains(usage, ".csv / .xlsx / .xls") {
		t.Errorf("fees import help still claims .xls is accepted (#92 refuses it):\n%s", usage)
	}
	for _, want := range []string{".csv / .xlsx", "不受支持"} {
		if !strings.Contains(usage, want) {
			t.Errorf("fees import help lacks %q:\n%s", want, usage)
		}
	}
}

// ---- helpers ------------------------------------------------------------

// cliErrorMessage decodes the stdout JSON error envelope and returns the
// message (usage errors must always answer the envelope contract, §10).
func cliErrorMessage(t *testing.T, stdout *bytes.Buffer) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode CLI stdout %q: %v", stdout.String(), err)
	}
	if env.Error.Code != "usage_error" {
		t.Fatalf("CLI stdout error.code = %q, want usage_error (stdout: %s)", env.Error.Code, stdout.String())
	}
	return env.Error.Message
}

// usageErrorMessage extracts the message of a dispatcher-returned usage error.
func usageErrorMessage(t *testing.T, err error) string {
	t.Helper()
	var uerr *usageError
	if !errors.As(err, &uerr) {
		t.Fatalf("dispatcher returned %v, want a *usageError", err)
	}
	return uerr.msg
}

// switchCases go/ast-parses file (package-relative) and returns the string
// literals of the first switch statement inside fn whose cases are string
// literals — the dispatch switch of every CLI dispatcher. This is the
// structural truth the usage strings are pinned against: a switch cannot be
// reflected at runtime, but its source can be read (the same
// read-the-source trick as mapping's four-point gate reading the TS mirror).
func switchCases(t *testing.T, file, fn string) []string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var decl *ast.FuncDecl
	for _, d := range parsed.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == fn {
			decl = fd
			break
		}
	}
	if decl == nil {
		t.Fatalf("func %s not found in %s", fn, file)
	}
	var cases []string
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		var literals []string
		for _, cl := range sw.Body.List {
			cc, ok := cl.(*ast.CaseClause)
			if !ok || len(cc.List) != 1 {
				continue
			}
			lit, ok := cc.List[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				literals = append(literals, s)
			}
		}
		if len(literals) > 0 && cases == nil {
			cases = literals
		}
		return true
	})
	if len(cases) == 0 {
		t.Fatalf("no string-literal switch found inside %s in %s", fn, file)
	}
	return cases
}

// claimedSubcommands extracts the subcommand list a usage message claims.
// All message shapes post-#108 derive their list from the dispatcher slice,
// so the segment formats are stable:
//
//	…可用：a, b, c                  (missing subcommand, top-level/lab/templates)
//	缺少 X 子命令（a / b / c）       (missing subcommand, splitSubArgs/splitFeeArgs)
//	X 子命令必须是 a、b 或 c，收到 %q (unknown subcommand, the default branch)
func claimedSubcommands(msg string) []string {
	if i := strings.Index(msg, "可用："); i >= 0 {
		return splitAndTrim(msg[i+len("可用："):], ",")
	}
	if open := strings.Index(msg, "（"); open >= 0 {
		if end := strings.Index(msg[open:], "）"); end > 0 {
			return splitAndTrim(msg[open+len("（"):open+end], " / ")
		}
	}
	if i := strings.Index(msg, "必须是 "); i >= 0 {
		rest := msg[i+len("必须是 "):]
		if j := strings.Index(rest, "，收到"); j >= 0 {
			rest = rest[:j]
		}
		return splitAndTrim(strings.ReplaceAll(rest, " 或 ", "、"), "、")
	}
	return nil
}

// typesClaimedInError extracts the registry enumeration from a --type
// validation error of the shape "--type 必须是注册资源类型之一（a/b/…），收到 %q".
func typesClaimedInError(t *testing.T, msg string) []string {
	t.Helper()
	open := strings.Index(msg, "（")
	if open < 0 {
		t.Fatalf("--type error carries no closed-set parentheses: %q", msg)
	}
	rest := msg[open+len("（"):]
	end := strings.Index(rest, "）")
	if end < 0 {
		t.Fatalf("--type error carries no closing parenthesis: %q", msg)
	}
	return splitAndTrim(rest[:end], "/")
}

// splitAndTrim splits s on sep, trimming each part and dropping empties.
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// assertSameSet fails unless want and got hold exactly the same strings —
// the bidirectional pin: a case missing from the message AND a message
// claiming a non-dispatching name both fail.
func assertSameSet(t *testing.T, ctx string, want, got []string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: no claimed subcommands extracted — the message drifted from the three known shapes", ctx)
	}
	w := slices.Clone(want)
	slices.Sort(w)
	g := slices.Clone(got)
	slices.Sort(g)
	if !slices.Equal(w, g) {
		t.Errorf("%s: usage claims %v, the dispatch switch holds %v", ctx, got, want)
	}
}
