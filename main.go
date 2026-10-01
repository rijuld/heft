// Command heft weighs a Go program's dependencies against what it actually uses.
//
//	heft [flags] [packages]              weigh every direct dependency
//	heft why <module> [packages]         show exactly what you use from one module
//	heft try <module>[@version]          weigh a module before you add it
//	heft extract <module> [packages]     print the code you use from a module
//	heft mcp                             serve heft to agents over MCP (stdio)
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/rijuld/heft/internal/weigh"
)

// version is set by the release build; otherwise it comes from the module
// version go install recorded.
var version = ""

// stdin is where heft mcp reads requests; tests replace it.
var stdin io.Reader = os.Stdin

const usage = `heft: weigh your Go dependencies against what you actually use

USAGE
    heft [flags] [packages]           weigh the direct dependencies of a program
    heft why <module> [packages]      show what you use from one module, and where
    heft try <module>[@version]       weigh a module before adding it (-use picks what you'd call)
    heft extract <module> [packages]  print the functions you use from a module, to copy
    heft mcp                          serve heft's tools to agents over MCP (stdio)
    heft -base <git-ref> [packages]   show what changed since a commit or branch

    packages defaults to ./... (every main package in the module).
    Flags may come before or after the other arguments; -- ends them.

OUTPUT
    -format f    text (default) | md | json
    -json        same as -format json
    -all         also list indirect modules
    -sort key    drops (default) | reach | name | verdict
    -no-color    plain output (also honours NO_COLOR)
    -v           print progress and timings to stderr
    -version     print heft's version

CI
    -fail-on vs  exit 1 if any direct dependency gets one of these verdicts
                 (comma-separated: inline,heavy,init-only,no-calls,shared,light,keep)
    -allow ms    modules -fail-on should accept anyway (comma-separated paths)

LOADING
    -tags list   build tags, as for go build
    -C dir       run in dir, as for go build
    -toolchain t local (default): never download the Go toolchain a go.mod asks for
                 auto: let the go command do so
    -offline     never download modules (GOPROXY=off)
    -cgo=false   never run the C toolchain (CGO_ENABLED=0)

TRY
    -use syms    what you'd call, e.g. -use semver.Compare,semver.IsValid
                 (default: every exported function of the module's root package)

VERDICTS
    ✂ inline     you call a few small functions and it brings nothing else along
    ⚠ heavy      it drags in several modules or a lot of code for little use
    ◌ init-only  nothing is called; only its init() runs
    ◌ no-calls   nothing is called; used for types or constants only
    ⇄ shared     other dependencies need it too; dropping your import frees nothing
    ~ light      you reach under 10% of its code
    ✓ keep       earns its place

EXIT CODES
    0 ok · 1 a -fail-on verdict matched · 2 usage error, or the program doesn't load

heft analyzes programs (main packages), using Rapid Type Analysis from
main.init and main.main. Library mode is on the roadmap.
`

type config struct {
	format    string
	all       bool
	sort      string
	noColor   bool
	verbose   bool
	failOn    []string
	allow     []string
	use       []string
	base      string
	opts      weigh.Options
	stdout    io.Writer
	stderr    io.Writer
	colorable bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, pos, code := parse(args, stdout, stderr)
	if cfg == nil {
		return code
	}
	cmd := ""
	if len(pos) > 0 && slices.Contains([]string{"why", "try", "extract", "mcp", "version"}, pos[0]) {
		cmd, pos = pos[0], pos[1:]
	}
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "heft", heftVersion())
		return 0
	case "mcp":
		return serveMCP(stdin, stdout, stderr, cfg)
	case "why", "try", "extract":
		if len(pos) == 0 || strings.TrimSpace(pos[0]) == "" {
			fmt.Fprintf(stderr, "heft %s: which module? e.g. heft %s github.com/spf13/cobra\n", cmd, cmd)
			return 2
		}
	}
	if cmd == "try" {
		cfg.opts.Patterns = nil
		return cmdTry(cfg, pos[0], pos[1:])
	}
	if cmd != "" {
		cfg.opts.Patterns = pos[1:]
	} else {
		cfg.opts.Patterns = pos
	}

	if cfg.base != "" {
		if cmd != "" {
			fmt.Fprintf(stderr, "heft: -base works on the report, not on %s\n", cmd)
			return 2
		}
		return cmdDiff(cfg)
	}

	fmt.Fprintln(stderr, "heft: loading and analyzing (this compiles type info for every dependency)…")
	rep, err := weigh.Analyze(cfg.opts)
	if err != nil {
		fmt.Fprintf(stderr, "heft: %v\n", err)
		return 2
	}

	switch cmd {
	case "why":
		m, err := rep.Lookup(pos[0])
		if err != nil {
			fmt.Fprintf(stderr, "heft: %v\n", err)
			return 2
		}
		switch cfg.format {
		case "json":
			return writeJSON(stdout, stderr, whyJSON(rep, m))
		case "md":
			renderWhyMarkdown(stdout, rep, m)
		default:
			renderWhy(stdout, cfg.painter(), rep, m)
		}
		return 0
	case "extract":
		m, err := rep.Lookup(pos[0])
		if err != nil {
			fmt.Fprintf(stderr, "heft: %v\n", err)
			return 2
		}
		src, err := rep.Extract(m)
		if err != nil {
			fmt.Fprintf(stderr, "heft: %v\n", err)
			return 2
		}
		fmt.Fprint(stdout, src)
		return 0
	}

	sortDirect(rep.Direct, cfg.sort)
	switch cfg.format {
	case "json":
		if code := writeJSON(stdout, stderr, rep); code != 0 {
			return code
		}
	case "md":
		renderMarkdown(stdout, rep, cfg.all)
	default:
		renderReport(stdout, cfg.painter(), rep, cfg.all)
	}
	return cfg.gate(rep.Direct)
}

// parse reads flags wherever they appear among the arguments. On failure it
// returns a nil config and the exit code.
func parse(args []string, stdout, stderr io.Writer) (*config, []string, int) {
	fs := flag.NewFlagSet("heft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	jsonOut := fs.Bool("json", false, "")
	format := fs.String("format", "text", "")
	all := fs.Bool("all", false, "")
	sortBy := fs.String("sort", "drops", "")
	noColor := fs.Bool("no-color", false, "")
	verbose := fs.Bool("v", false, "")
	showVersion := fs.Bool("version", false, "")
	failOn := fs.String("fail-on", "", "")
	allow := fs.String("allow", "", "")
	use := fs.String("use", "", "")
	base := fs.String("base", "", "")
	tags := fs.String("tags", "", "")
	dir := fs.String("C", "", "")
	toolchain := fs.String("toolchain", "local", "")
	offline := fs.Bool("offline", false, "")
	cgo := fs.Bool("cgo", true, "")

	var pos []string
	if i := slices.Index(args, "--"); i >= 0 {
		pos = append(pos, args[i+1:]...)
		args = args[:i]
	}
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, nil, 0
			}
			return nil, nil, 2
		}
		if fs.NArg() == 0 {
			break
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
	pos = append(rest, pos...)

	if *showVersion {
		fmt.Fprintln(stdout, "heft", heftVersion())
		return nil, nil, 0
	}
	if *jsonOut {
		*format = "json"
	}
	bad := func(format string, a ...any) (*config, []string, int) {
		fmt.Fprintf(stderr, "heft: "+format+"\n", a...)
		return nil, nil, 2
	}
	if !slices.Contains([]string{"text", "md", "json"}, *format) {
		return bad("unknown -format %q (want text, md or json)", *format)
	}
	if !slices.Contains([]string{"drops", "reach", "name", "verdict"}, *sortBy) {
		return bad("unknown -sort %q (want drops, reach, name or verdict)", *sortBy)
	}
	if *toolchain != "local" && *toolchain != "auto" {
		return bad("unknown -toolchain %q (want local or auto)", *toolchain)
	}
	cfg := &config{
		format: *format, all: *all, sort: *sortBy, noColor: *noColor, verbose: *verbose,
		failOn: list(*failOn), allow: list(*allow), use: list(*use), base: *base,
		stdout: stdout, stderr: stderr,
		colorable: !*noColor && os.Getenv("NO_COLOR") == "" && isTerminal(stdout),
		opts: weigh.Options{
			Dir: *dir, Tags: *tags,
			AutoToolchain: *toolchain == "auto", Offline: *offline, NoCgo: !*cgo,
		},
	}
	for _, v := range cfg.failOn {
		if !slices.Contains(weigh.Verdicts, v) {
			// A typo must not turn into a CI guard that never fires.
			return bad("unknown verdict %q for -fail-on (want one of %s)", v, strings.Join(weigh.Verdicts, ", "))
		}
	}
	if *verbose {
		cfg.opts.Log = func(format string, a ...any) { fmt.Fprintf(stderr, "heft: "+format+"\n", a...) }
	}
	return cfg, pos, 0
}

func list(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (c *config) painter() painter { return painter(c.colorable) }

// allowed reports whether -allow names the module (by path or path suffix).
func (c *config) allowed(path string) bool {
	for _, a := range c.allow {
		if path == a || strings.HasSuffix(path, "/"+a) {
			return true
		}
	}
	return false
}

// gate applies -fail-on, naming every offender.
func (c *config) gate(direct []*weigh.Dep) int {
	if len(c.failOn) == 0 {
		return 0
	}
	code := 0
	for _, d := range direct {
		if !slices.Contains(c.failOn, d.Verdict) {
			continue
		}
		if c.allowed(d.Path) {
			fmt.Fprintf(c.stderr, "heft: %s is %q, allowed by -allow\n", d.Path, d.Verdict)
			continue
		}
		fmt.Fprintf(c.stderr, "heft: %s is %q: %s\n", d.Path, d.Verdict, d.Note)
		code = 1
	}
	return code
}

var verdictRank = func() map[string]int {
	m := map[string]int{}
	for i, v := range weigh.Verdicts {
		m[v] = i
	}
	return m
}()

func sortDirect(ds []*weigh.Dep, by string) {
	slices.SortStableFunc(ds, func(a, b *weigh.Dep) int {
		switch by {
		case "reach":
			if a.ReachedLines != b.ReachedLines {
				return b.ReachedLines - a.ReachedLines
			}
		case "name":
			return strings.Compare(a.Path, b.Path)
		case "verdict":
			if a.Verdict != b.Verdict {
				return verdictRank[a.Verdict] - verdictRank[b.Verdict]
			}
		}
		if a.DropLines != b.DropLines {
			return b.DropLines - a.DropLines
		}
		return strings.Compare(a.Path, b.Path)
	})
}

func writeJSON(w, stderr io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "heft: %v\n", err)
		return 2
	}
	return 0
}

func heftVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
