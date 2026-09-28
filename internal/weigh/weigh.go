// Package weigh measures how much of each dependency a Go program actually
// uses, and how much it drags in.
//
// It loads the program with go/packages, builds SSA, and runs Rapid Type
// Analysis (RTA) from main.init and main.main, the same approach as
// golang.org/x/tools/cmd/deadcode. Every source-level function is then
// attributed to the module that defines it, reachable or not.
package weigh

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Options control how the program is loaded.
type Options struct {
	Dir      string   // working directory (default: current)
	Patterns []string // package patterns (default: ./...)
	Tags     string   // comma-separated build tags
}

// Func is one source-level function or method declaration.
type Func struct {
	Name    string         `json:"name"`
	Package string         `json:"package"`
	Pos     token.Position `json:"-"`
	File    string         `json:"file"`
	Line    int            `json:"line"`
	Lines   int            `json:"lines"`
	Reached bool           `json:"reached"`
	Init    bool           `json:"-"`
}

// Module is everything heft knows about one module in the build.
type Module struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Replace string `json:"replace,omitempty"`
	Direct  bool   `json:"direct"`

	Packages     int `json:"packages"`      // packages compiled into the program
	Lines        int `json:"lines"`         // source lines in those packages
	Funcs        int `json:"funcs"`         // declared functions and methods (excluding init)
	ReachedFuncs int `json:"reached_funcs"` // of those, reachable from main
	FuncLines    int `json:"func_lines"`    // lines inside function declarations
	ReachedLines int `json:"reached_lines"` // lines inside reachable declarations
	InitFuncs    int `json:"init_funcs"`    // init() functions (they always run)
	functions    []*Func
	packages     []string
}

// Dep is a direct dependency with its removal cost.
type Dep struct {
	*Module
	// Drops lists modules that disappear from the build if this dependency is
	// removed (itself included).
	Drops []string `json:"drops"`
	// DropLines counts source lines in every package that would disappear.
	DropLines int `json:"drop_lines"`
	// DropFuncLines counts lines inside function declarations among them,
	// and DropReachedLines those inside reachable ones.
	DropFuncLines    int    `json:"drop_func_lines"`
	DropReachedLines int    `json:"drop_reached_lines"`
	Verdict          string `json:"verdict"`
	Note             string `json:"note"`
}

// Entry is a call from outside a module into it.
type Entry struct {
	Callee string `json:"callee"`
	Caller string `json:"caller"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// Report is the result of weighing one program (or several main packages).
type Report struct {
	MainModule     string    `json:"main_module"`
	Programs       []string  `json:"programs"`
	ThirdParty     int       `json:"third_party_lines"`
	ThirdReached   int       `json:"third_party_reached_lines"`
	ThirdFuncLines int       `json:"third_party_func_lines"`
	Modules        []*Module `json:"modules"`
	Direct         []*Dep    `json:"direct"`

	byPath  map[string]*Module
	graph   *callgraph.Graph
	fset    *token.FileSet
	ownerOf func(*ssa.Function) (*Module, string)
}

// Verdicts, strongest first.
const (
	VerdictInline   = "inline"
	VerdictHeavy    = "heavy"
	VerdictInitOnly = "init-only"
	VerdictNoCalls  = "no-calls"
	VerdictShared   = "shared"
	VerdictLight    = "light"
	VerdictKeep     = "keep"
)

// Verdicts lists every verdict heft can give.
var Verdicts = []string{VerdictInline, VerdictHeavy, VerdictInitOnly, VerdictNoCalls, VerdictShared, VerdictLight, VerdictKeep}

// ErrNoMain is returned when the patterns match no main package.
var ErrNoMain = errors.New("no main packages found: heft weighs programs (try ./cmd/yourtool or ./...)")

// Analyze loads the program and weighs its dependencies.
func Analyze(opt Options) (*Report, error) {
	patterns := opt.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	cfg := &packages.Config{
		Mode: packages.LoadAllSyntax | packages.NeedModule,
		Dir:  opt.Dir,
	}
	if opt.Tags != "" {
		cfg.BuildFlags = []string{"-tags=" + opt.Tags}
	}
	initial, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	var loadErrs []string
	packages.Visit(initial, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		if len(loadErrs) > 10 {
			loadErrs = append(loadErrs[:10], fmt.Sprintf("... and %d more", len(loadErrs)-10))
		}
		return nil, fmt.Errorf("packages have errors:\n  %s", strings.Join(loadErrs, "\n  "))
	}

	prog, ssaPkgs := ssautil.AllPackages(initial, ssa.InstantiateGenerics)
	prog.Build()

	var roots []*ssa.Function
	var mainPkgs []*packages.Package
	for i, p := range initial {
		if p.Name == "main" && ssaPkgs[i] != nil && ssaPkgs[i].Func("main") != nil {
			roots = append(roots, ssaPkgs[i].Func("init"), ssaPkgs[i].Func("main"))
			mainPkgs = append(mainPkgs, p)
		}
	}
	if len(roots) == 0 {
		return nil, ErrNoMain
	}

	res := rta.Analyze(roots, true)
	res.CallGraph.DeleteSyntheticNodes()

	// Reachability by position: generic instances and wrappers collapse
	// onto the declaration they came from.
	reached := make(map[token.Position]bool)
	for fn := range res.Reachable {
		if fn.Pos().IsValid() {
			reached[prog.Fset.Position(fn.Pos())] = true
		}
	}

	r := &Report{byPath: map[string]*Module{}, graph: res.CallGraph, fset: prog.Fset}
	for _, p := range mainPkgs {
		r.Programs = append(r.Programs, p.PkgPath)
		if p.Module != nil {
			r.MainModule = p.Module.Path
		}
	}

	all := map[string]*packages.Package{}
	pkgModule := map[string]*Module{} // package path → module (nil for std / main)
	packages.Visit(mainPkgs, nil, func(p *packages.Package) {
		all[p.PkgPath] = p
		m := p.Module
		if m == nil || m.Main {
			return
		}
		mod := r.byPath[m.Path]
		if mod == nil {
			mod = &Module{Path: m.Path, Version: m.Version}
			if m.Replace != nil {
				mod.Replace = m.Replace.Path
				if m.Replace.Version != "" {
					mod.Replace += "@" + m.Replace.Version
				}
			}
			r.byPath[m.Path] = mod
			r.Modules = append(r.Modules, mod)
		}
		pkgModule[p.PkgPath] = mod
		mod.Packages++
		mod.packages = append(mod.packages, p.PkgPath)
		for _, f := range p.Syntax {
			mod.Lines += prog.Fset.File(f.Pos()).LineCount()
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				fn := &Func{
					Name:    funcName(fd),
					Package: p.PkgPath,
					Pos:     prog.Fset.Position(fd.Name.Pos()),
					Lines:   prog.Fset.Position(fd.End()).Line - prog.Fset.Position(fd.Pos()).Line + 1,
					Init:    fd.Recv == nil && fd.Name.Name == "init",
				}
				fn.File, fn.Line = fn.Pos.Filename, fn.Pos.Line
				fn.Reached = fn.Init || reached[fn.Pos]
				if obj, ok := p.TypesInfo.Defs[fd.Name].(*types.Func); ok && !fn.Reached {
					if v := prog.FuncValue(obj); v != nil && v.Pos().IsValid() {
						fn.Reached = reached[prog.Fset.Position(v.Pos())]
					}
				}
				mod.functions = append(mod.functions, fn)
				if fn.Init {
					mod.InitFuncs++
					continue
				}
				mod.Funcs++
				mod.FuncLines += fn.Lines
				if fn.Reached {
					mod.ReachedFuncs++
					mod.ReachedLines += fn.Lines
				}
			}
		}
	})

	for _, m := range r.Modules {
		r.ThirdParty += m.Lines
		r.ThirdFuncLines += m.FuncLines
		r.ThirdReached += m.ReachedLines
	}

	// Direct dependencies: modules imported by the main module's own packages.
	for _, p := range all {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		for _, imp := range p.Imports {
			if m := pkgModule[imp.PkgPath]; m != nil {
				m.Direct = true
			}
		}
	}

	// What disappears if you delete your imports of a direct dependency?
	// Walk the package import graph from the main packages, cutting only the
	// edges from the main module into that dependency; other dependencies may
	// still need it.
	isMain := func(p *packages.Package) bool { return p.Module != nil && p.Module.Main }
	full := reach(mainPkgs, func(_, _ *packages.Package) bool { return true })
	for _, m := range r.Modules {
		if !m.Direct {
			continue
		}
		kept := reach(mainPkgs, func(from, to *packages.Package) bool {
			return !isMain(from) || pkgModule[to.PkgPath] != m
		})
		d := &Dep{Module: m}
		keptModules := map[*Module]bool{}
		for path := range kept {
			if km := pkgModule[path]; km != nil {
				keptModules[km] = true
			}
		}
		dropped := map[*Module]bool{}
		for path := range full {
			if kept[path] {
				continue
			}
			mod := pkgModule[path]
			if mod == nil {
				continue // std or main
			}
			for _, f := range all[path].Syntax {
				d.DropLines += prog.Fset.File(f.Pos()).LineCount()
			}
			for _, fn := range mod.functions {
				if fn.Package != path || fn.Init {
					continue
				}
				d.DropFuncLines += fn.Lines
				if fn.Reached {
					d.DropReachedLines += fn.Lines
				}
			}
			if !keptModules[mod] {
				dropped[mod] = true
			}
		}
		for mod := range dropped {
			d.Drops = append(d.Drops, mod.Path)
		}
		sort.Slice(d.Drops, func(i, j int) bool {
			if d.Drops[i] == m.Path || d.Drops[j] == m.Path {
				return d.Drops[i] == m.Path
			}
			return d.Drops[i] < d.Drops[j]
		})
		d.Verdict, d.Note = verdict(d)
		r.Direct = append(r.Direct, d)
	}

	sort.Slice(r.Modules, func(i, j int) bool { return r.Modules[i].Path < r.Modules[j].Path })
	sort.Slice(r.Direct, func(i, j int) bool {
		if r.Direct[i].DropLines != r.Direct[j].DropLines {
			return r.Direct[i].DropLines > r.Direct[j].DropLines
		}
		return r.Direct[i].Path < r.Direct[j].Path
	})

	r.ownerOf = func(fn *ssa.Function) (*Module, string) {
		for fn.Parent() != nil {
			fn = fn.Parent()
		}
		if o := fn.Origin(); o != nil {
			fn = o
		}
		if fn.Pkg == nil {
			return nil, ""
		}
		return pkgModule[fn.Pkg.Pkg.Path()], fn.Pkg.Pkg.Path()
	}
	return r, nil
}

// reach returns the packages reachable from roots through imports that
// follow returns true for.
func reach(roots []*packages.Package, follow func(from, to *packages.Package) bool) map[string]bool {
	seen := map[string]bool{}
	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		if seen[p.PkgPath] {
			return
		}
		seen[p.PkgPath] = true
		for _, imp := range p.Imports {
			if follow(p, imp) {
				walk(imp)
			}
		}
	}
	for _, p := range roots {
		walk(p)
	}
	return seen
}

// Thresholds behind the verdicts. Deliberately conservative: "inline" should
// only fire when copying the code is obviously cheaper than depending on it.
const (
	inlineMaxFuncs  = 3
	inlineMaxLines  = 60
	heavyMinModules = 4     // drags in at least this many modules…
	heavyMinLines   = 20000 // …or this many lines…
	heavyMaxUsePct  = 25    // …and you reach less than this share of it
	lightMaxUsePct  = 10
)

func verdict(d *Dep) (string, string) {
	pct := percent(d.ReachedLines, d.FuncLines)
	switch {
	case d.ReachedFuncs == 0 && d.InitFuncs > 0:
		return VerdictInitOnly, "no functions called; only its init() runs (imported for side effects?)"
	case d.ReachedFuncs == 0:
		return VerdictNoCalls, "no functions reached; used only for types or constants"
	case len(d.Drops) == 0:
		return VerdictShared, "other dependencies import it too, so dropping your import frees nothing"
	case d.ReachedFuncs <= inlineMaxFuncs && d.ReachedLines <= inlineMaxLines && len(d.Drops) == 1:
		return VerdictInline, fmt.Sprintf("you use %d line%s of it; consider copying them (keep the license)", d.ReachedLines, plural(d.ReachedLines))
	case (len(d.Drops) >= heavyMinModules || d.DropLines >= heavyMinLines) && percent(d.DropReachedLines, d.DropFuncLines) < heavyMaxUsePct:
		return VerdictHeavy, fmt.Sprintf("brings %d modules / %s lines; you reach %s of their %s function lines (%.0f%%)",
			len(d.Drops), Human(d.DropLines), Human(d.DropReachedLines), Human(d.DropFuncLines), percent(d.DropReachedLines, d.DropFuncLines))
	case pct < lightMaxUsePct:
		return VerdictLight, fmt.Sprintf("you reach %.0f%% of its code", pct)
	default:
		return VerdictKeep, fmt.Sprintf("you reach %.0f%% of its code", pct)
	}
}

// Lookup finds a module by exact path, then by path suffix ("cobra" →
// github.com/spf13/cobra, even when github.com/muesli/mango-cobra exists),
// then by substring.
func (r *Report) Lookup(query string) (*Module, error) {
	if m := r.byPath[query]; m != nil {
		return m, nil
	}
	match := func(ok func(path string) bool) []*Module {
		var hits []*Module
		for _, m := range r.Modules {
			if ok(m.Path) {
				hits = append(hits, m)
			}
		}
		return hits
	}
	hits := match(func(p string) bool { return strings.HasSuffix(p, "/"+query) })
	if len(hits) == 0 {
		hits = match(func(p string) bool { return strings.Contains(p, query) })
	}
	switch len(hits) {
	case 0:
		return nil, fmt.Errorf("no module matching %q in this program", query)
	case 1:
		return hits[0], nil
	default:
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = h.Path
		}
		return nil, fmt.Errorf("%q matches several modules: %s", query, strings.Join(names, ", "))
	}
}

// Dep returns the direct-dependency record for m, if m is direct.
func (r *Report) Dep(m *Module) *Dep {
	for _, d := range r.Direct {
		if d.Module == m {
			return d
		}
	}
	return nil
}

// Functions returns m's declared functions, reached ones first, biggest first.
func (m *Module) Functions() []*Func {
	out := append([]*Func(nil), m.functions...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Reached != out[j].Reached {
			return out[i].Reached
		}
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Entries lists calls from outside m into m: where your program touches it.
func (r *Report) Entries(m *Module) []Entry {
	seen := map[string]bool{}
	var out []Entry
	for fn, node := range r.graph.Nodes {
		if fn == nil {
			continue
		}
		owner, _ := r.ownerOf(fn)
		if owner != m || (fn.Name() == "init" && fn.Parent() == nil && fn.Synthetic != "") {
			continue // package initializers are implicit, not calls you wrote
		}
		for _, e := range node.In {
			if e.Caller == nil || e.Caller.Func == nil {
				continue
			}
			if callerMod, _ := r.ownerOf(e.Caller.Func); callerMod == m {
				continue
			}
			pos := r.fset.Position(e.Pos())
			if !pos.IsValid() {
				continue // synthetic edges, e.g. reflect.Value.Call in RTA's model
			}
			ent := Entry{Callee: ssaName(fn), Caller: ssaName(e.Caller.Func), File: pos.Filename, Line: pos.Line}
			key := ent.Callee + "|" + ent.Caller
			if !seen[key] {
				seen[key] = true
				out = append(out, ent)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Callee != out[j].Callee {
			return out[i].Callee < out[j].Callee
		}
		return out[i].Caller < out[j].Caller
	})
	return out
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	t := fd.Recv.List[0].Type
	star := ""
	if s, ok := t.(*ast.StarExpr); ok {
		star, t = "*", s.X
	}
	switch x := t.(type) {
	case *ast.IndexExpr:
		t = x.X
	case *ast.IndexListExpr:
		t = x.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return "(" + star + id.Name + ")." + fd.Name.Name
	}
	return fd.Name.Name
}

func ssaName(fn *ssa.Function) string {
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	if o := fn.Origin(); o != nil {
		fn = o
	}
	name := fn.Name()
	if recv := fn.Signature.Recv(); recv != nil {
		t := recv.Type()
		star := ""
		if p, ok := t.(*types.Pointer); ok {
			star, t = "*", p.Elem()
		}
		if n, ok := t.(*types.Named); ok {
			name = "(" + star + n.Obj().Name() + ")." + name
		}
	}
	if fn.Pkg != nil {
		return fn.Pkg.Pkg.Name() + "." + name
	}
	return name
}

func percent(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Human formats a count compactly: 950, 12.3k, 1.2M.
func Human(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprint(n)
	}
}
