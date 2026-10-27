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
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
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
	dir := testsupport.TempDir(t)
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
		{"metrics", "metrics_cli.go", "cmdMetrics", cmdMetrics, nil, []string{"--db", db, "__nope__"}},
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

// TestFeesImportHelpNoLongerClaimsXLS pins #108-C: the fees import help must
// match the #92 truth — .xls is REFUSED (Excel 2003 binary; the reader and
// the snapshot layer both reject it with a dedicated message), so the help
// claiming ".csv / .xlsx / .xls" was a lie the operator discovered only at
// import time.
func TestFeesImportHelpNoLongerClaimsXLS(t *testing.T) {
	dir := testsupport.TempDir(t)
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
