package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const app = "internal/weigh/testdata/app"

func runHeft(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestReportText(t *testing.T) {
	code, out, errs := runHeft("-C", app)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"example.com/tiny v0.0.0", "✂ inline candidate",
		"example.com/big v0.0.0", "⚠ heavy for what you use",
		"drops with it: example.com/deep1, example.com/deep2, example.com/huge",
		"⇄ needed by other deps too", "◌ only init() runs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("colour codes written to a non-terminal")
	}
}

func TestWhyFlagsEitherSide(t *testing.T) {
	for _, args := range [][]string{{"-C", app, "why", "tiny"}, {"why", "tiny", "-C", app}} {
		code, out, errs := runHeft(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs)
		}
		if !strings.Contains(out, "tiny.PadLeft  ← main.main") {
			t.Errorf("%v: missing entry point:\n%s", args, out)
		}
	}
}

func TestJSON(t *testing.T) {
	code, out, errs := runHeft("-C", app, "-json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var v struct {
		Direct []struct {
			Path    string   `json:"path"`
			Verdict string   `json:"verdict"`
			Drops   []string `json:"drops"`
		} `json:"direct"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if len(v.Direct) != 5 || v.Direct[0].Path != "example.com/big" || v.Direct[0].Verdict != "heavy" {
		t.Fatalf("unexpected: %+v", v.Direct)
	}
}

func TestFailOn(t *testing.T) {
	if code, _, _ := runHeft("-C", app, "-fail-on", "heavy"); code != 1 {
		t.Fatalf("want exit 1 for -fail-on heavy, got %d", code)
	}
	if code, _, _ := runHeft("-C", app, "-fail-on", "no-calls"); code != 0 {
		t.Fatalf("want exit 0 for -fail-on no-calls, got %d", code)
	}
	// A typo must not turn into a CI guard that never fires.
	if code, _, errs := runHeft("-C", app, "-fail-on", "heavey"); code != 2 || !strings.Contains(errs, "unknown verdict") {
		t.Fatalf("want exit 2 for a misspelled verdict, got %d: %s", code, errs)
	}
}

func TestErrors(t *testing.T) {
	if code, _, errs := runHeft("-C", "internal/weigh/testdata/lib"); code != 2 || !strings.Contains(errs, "no main packages") {
		t.Fatalf("library: exit %d, %s", code, errs)
	}
	if code, _, _ := runHeft("-C", app, "why"); code != 2 {
		t.Fatalf("why without module: exit %d", code)
	}
	if code, _, errs := runHeft("-C", app, "why", "nope"); code != 2 || !strings.Contains(errs, "no module matching") {
		t.Fatalf("why nope: exit %d, %s", code, errs)
	}
}

func TestFlagsAfterPackages(t *testing.T) {
	code, out, errs := runHeft("-C", app, ".", "-json", "-sort", "name")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var v struct {
		Direct []struct{ Path string } `json:"direct"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("-json after the package was not honoured: %v", err)
	}
	if v.Direct[0].Path != "example.com/big" || v.Direct[4].Path != "example.com/tiny" {
		t.Fatalf("-sort name: %+v", v.Direct)
	}
	// After --, everything is a package pattern.
	if code, _, errs := runHeft("-C", app, "--", "-json"); code != 2 || !strings.Contains(errs, "-json") {
		t.Fatalf("-- should end flags: exit %d: %s", code, errs)
	}
}

func TestFailOnListAndAllow(t *testing.T) {
	code, _, errs := runHeft("-C", app, "-fail-on", "heavy,inline", "-allow", "mega-tiny")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{`example.com/big is "heavy"`, `example.com/tiny is "inline"`, `example.com/mega-tiny is "inline", allowed`} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr missing %q:\n%s", want, errs)
		}
	}
	if code, _, _ := runHeft("-C", app, "-fail-on", "heavy", "-allow", "example.com/big"); code != 0 {
		t.Fatalf("everything allowed: exit %d", code)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"-C", app, "why", ""},
		{"-C", app, "-format", "yaml"},
		{"-C", app, "-sort", "size"},
		{"-C", app, "-toolchain", "latest"},
		{"-C", app, "try"},
	} {
		if code, _, _ := runHeft(args...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"-version"}, {"version"}} {
		if code, out, _ := runHeft(args...); code != 0 || !strings.HasPrefix(out, "heft ") {
			t.Errorf("%v: exit %d, %q", args, code, out)
		}
	}
}

func TestMarkdown(t *testing.T) {
	code, out, errs := runHeft("-C", app, "-format", "md")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"| Direct dependency | You call |",
		"| `example.com/tiny` v0.0.0 | 1 of 3 funcs | 6 lines (40%) | 1 module · 23 lines | MIT | ✂ inline candidate |",
		"| `example.com/mega-tiny` v0.0.0 | 1 of 1 funcs | 1 line (100%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestWhyShowsLicense(t *testing.T) {
	_, out, _ := runHeft("-C", app, "why", "tiny")
	if !strings.Contains(out, "LICENSE MIT (LICENSE)") || !strings.Contains(out, "keep its MIT notice") {
		t.Fatalf("license missing:\n%s", out)
	}
}

var update = flag.Bool("update", false, "rewrite golden files")

// The JSON is an interface agents and CI scripts parse: any change to it
// must show up here, and renames or removals need a schema_version bump.
func TestGoldenJSON(t *testing.T) {
	abs, err := filepath.Abs("internal/weigh/testdata")
	if err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]string{
		"report.json":  {"-C", app, "-json", "-all"},
		"why-big.json": {"-C", app, "-json", "why", "big"},
	} {
		code, out, errs := runHeft(args...)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errs)
		}
		// Absolute paths differ per machine; JSON escapes Windows separators.
		out = strings.ReplaceAll(out, strings.ReplaceAll(abs, `\`, `\\`), "$TESTDATA")
		out = strings.ReplaceAll(out, `\\`, "/")
		golden := filepath.Join("testdata", name)
		if *update {
			if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test -update)", err)
		}
		if out != string(want) {
			t.Errorf("%s changed; if that's intended, run go test -update and check schema_version.\ngot:\n%s", name, out)
		}
	}
}

func TestTryLeavesYourModuleAlone(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	before := readAll(t, app+"/go.mod")
	code, out, errs := runHeft("-C", app, "try", "../big", "-use", "Greet", "-json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if after := readAll(t, app+"/go.mod"); after != before {
		t.Fatal("heft try changed go.mod")
	}
	if _, err := os.Stat(app + "/go.sum"); err == nil {
		t.Fatal("heft try wrote a go.sum")
	}
	var v struct {
		Use        []string `json:"use"`
		Compared   bool     `json:"compared_with_your_build"`
		NewModules []string `json:"new_modules"`
		Dep        struct {
			Verdict      string   `json:"verdict"`
			ReachedFuncs int      `json:"reached_funcs"`
			Drops        []string `json:"drops"`
		} `json:"dependency"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	// Alone, big brings shared along too; the app already has all of them.
	if v.Dep.Verdict != "heavy" || v.Dep.ReachedFuncs != 2 || len(v.Dep.Drops) != 5 || !v.Compared || len(v.NewModules) != 0 {
		t.Fatalf("try big: %+v", v)
	}
}

func TestTryNamesWhatItCannotCall(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	code, _, errs := runHeft("-C", app, "try", "../big", "-use", "engine.Render")
	if code != 2 || !strings.Contains(errs, "internal") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	code, _, errs = runHeft("-C", app, "try", "../big", "-use", "Nope")
	if code != 2 || !strings.Contains(errs, "Nope") {
		t.Fatalf("exit %d: %s", code, errs)
	}
}

// What heft extract prints must compile on its own for self-contained code.
func TestExtractBuilds(t *testing.T) {
	for _, tc := range []struct{ dir, module string }{
		{app, "tiny"},
		{"internal/weigh/testdata/edgeapp", "gen"},
		{"internal/weigh/testdata/edgeapp", "lit"},
	} {
		code, out, errs := runHeft("-C", tc.dir, "extract", tc.module)
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", tc.module, code, errs)
		}
		tmp := t.TempDir()
		os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module example.com/copied\n\ngo 1.26\n"), 0o644)
		os.WriteFile(filepath.Join(tmp, "copied.go"), []byte(out), 0o644)
		cmd := exec.Command("go", "build", "./...")
		cmd.Dir = tmp
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: extracted code doesn't build: %v\n%s\n%s", tc.module, err, b, out)
		}
	}
	if _, out, _ := runHeft("-C", app, "extract", "tiny"); !strings.Contains(out, "PadLeft") || strings.Contains(out, "PadRight") || !strings.Contains(out, "License (MIT)") {
		t.Fatalf("want only the reached PadLeft, with its license:\n%s", out)
	}
}

// -base weighs a git revision exported into a temporary directory.
func TestBaseDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if err := os.CopyFS(repo, os.DirFS("internal/weigh/testdata")); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=heft", "-c", "user.email=heft@example.com"}, args...)...)
		cmd.Dir = repo
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	main := filepath.Join(repo, "app", "main.go")
	src := readAll(t, main)
	without := strings.Replace(strings.Replace(src, "\t\"example.com/tiny\"\n", "", 1), "\tfmt.Println(tiny.PadLeft(name, 12))\n", "", 1)
	os.WriteFile(main, []byte(without), 0o644)
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	os.WriteFile(main, []byte(src), 0o644) // the change under review adds tiny

	dir := filepath.Join(repo, "app")
	code, out, errs := runHeft("-C", dir, "-base", "HEAD", "-json", "-fail-on", "inline")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (tiny is new and inline): %s", code, errs)
	}
	if !strings.Contains(errs, "example.com/tiny") || strings.Contains(errs, "mega-tiny") {
		t.Fatalf("only the new dependency should be gated:\n%s", errs)
	}
	var d struct {
		Added     []struct{ Path string } `json:"added_modules"`
		NewDirect []struct{ Path string } `json:"new_direct"`
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 1 || d.Added[0].Path != "example.com/tiny" || len(d.NewDirect) != 1 {
		t.Fatalf("diff: %+v", d)
	}
	if code, _, errs := runHeft("-C", dir, "-base", "no-such-ref"); code != 2 || !strings.Contains(errs, "not a commit") {
		t.Fatalf("bad ref: exit %d: %s", code, errs)
	}
}

func TestMCP(t *testing.T) {
	abs, _ := filepath.Abs(app)
	argsJSON, _ := json.Marshal(map[string]any{"module": "tiny", "dir": abs})
	probe := filepath.Join(t.TempDir(), "probe.tar")
	stdin = strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"heft_why","arguments":` + string(argsJSON) + `}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"heft_why","arguments":{"module":"nope","dir":` + strconv.Quote(abs) + `}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"bogus"}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
		// Agent-supplied strings must never reach go or git as flags.
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"heft_try","arguments":{"module":"-modfile=x.mod@v1","dir":` + strconv.Quote(abs) + `}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"heft_diff","arguments":{"base":` + strconv.Quote("--output="+probe) + `,"dir":` + strconv.Quote(abs) + `}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"heft_try","arguments":{"module":"../big","use":["-toolexec=x/y.F"],"dir":` + strconv.Quote(abs) + `}}}`,
	}, "\n") + "\n")
	defer func() { stdin = os.Stdin }()

	code, out, errs := runHeft("mcp")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 9 {
		t.Fatalf("want 9 responses (none for the notification), got %d:\n%s", len(lines), out)
	}
	type resp struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string           `json:"protocolVersion"`
			Tools           []map[string]any `json:"tools"`
			IsError         bool             `json:"isError"`
			Content         []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct{ Code int } `json:"error"`
	}
	var rs []resp
	for _, l := range lines {
		var r resp
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("stdout must carry only JSON-RPC: %q: %v", l, err)
		}
		rs = append(rs, r)
	}
	if rs[0].Result.ProtocolVersion != "2025-06-18" {
		t.Errorf("initialize: %+v", rs[0])
	}
	if len(rs[1].Result.Tools) != 5 {
		t.Errorf("tools/list: %d tools", len(rs[1].Result.Tools))
	}
	if rs[2].Result.IsError || !strings.Contains(rs[2].Result.Content[0].Text, `"callee": "tiny.PadLeft"`) {
		t.Errorf("heft_why: %+v", rs[2].Result)
	}
	if !rs[3].Result.IsError || !strings.Contains(rs[3].Result.Content[0].Text, "no module matching") {
		t.Errorf("tool error should be a result with isError: %+v", rs[3])
	}
	if rs[4].Error == nil || rs[4].Error.Code != -32601 {
		t.Errorf("unknown method: %+v", rs[4])
	}
	if rs[5].ID != 6 || rs[5].Error != nil {
		t.Errorf("ping: %+v", rs[5])
	}
	for _, r := range rs[6:] {
		text := r.Result.Content[0].Text
		if !r.Result.IsError || !strings.Contains(text, "invalid") && !strings.Contains(text, "not a commit") {
			t.Errorf("id %d: want the argument refused before it reaches go or git, got: %s", r.ID, text)
		}
	}
	if _, err := os.Stat(probe); err == nil {
		t.Error("git wrote the file an agent named in -base")
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
