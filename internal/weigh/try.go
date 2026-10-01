package weigh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// TryResult weighs a module you haven't added yet.
type TryResult struct {
	SchemaVersion int    `json:"schema_version"`
	Module        string `json:"module"`
	Version       string `json:"version"`
	// Use lists the calls heft made on your behalf, and Skipped the ones it
	// couldn't (with why).
	Use     []string `json:"use"`
	Skipped []string `json:"skipped,omitempty"`
	// Dep is the module as a direct dependency of a program that makes
	// exactly those calls.
	Dep *Dep `json:"dependency"`
	// AlreadyInBuild lists modules it would bring that your program already
	// has, and NewModules/NewLines what would really be new. Both are empty
	// when there was no program in Options.Dir to compare with.
	AlreadyInBuild []string `json:"already_in_build"`
	NewModules     []string `json:"new_modules"`
	NewLines       int      `json:"new_lines"`
	Compared       bool     `json:"compared_with_your_build"`
}

// Try weighs module (a path, path@version, or a local directory) as if your
// program called the functions in use. Each use is "Func", "Type.Method",
// "pkg.Func" or "pkg.Type.Method", where pkg is the last element of one of
// the module's package paths (or a full import path). With no use, it calls
// every exported function of the module's root package.
//
// It works in a temporary module; your go.mod and go.sum are never touched.
// Unless opt.Offline is set, the go command downloads what it needs.
func Try(module string, use []string, opt Options) (*TryResult, error) {
	tmp, err := os.MkdirTemp("", "heft-try-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	env := opt.Env()
	gocmd := func(args ...string) error {
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Env = tmp, env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return explain(fmt.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String())))
		}
		return nil
	}

	if err := gocmd("mod", "init", "heft.try"); err != nil {
		return nil, err
	}
	modPath, query, err := resolveTarget(module, opt.Dir)
	if err != nil {
		return nil, err
	}
	// Your replace directives would apply once you added it, so they apply
	// to the trial too.
	for _, r := range yourReplaces(opt) {
		if err := gocmd("mod", "edit", "-replace="+r); err != nil {
			return nil, err
		}
	}
	if dir, ok := strings.CutPrefix(query, "dir:"); ok {
		if err := gocmd("mod", "edit", "-replace="+modPath+"="+dir); err != nil {
			return nil, err
		}
		if err := gocmd("get", "--", modPath+"@v0.0.0"); err != nil {
			return nil, err
		}
	} else if err := gocmd("get", "--", query); err != nil {
		return nil, err
	}

	// Pass 1: types of the packages the calls name.
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedTypes | packages.NeedModule, Dir: tmp, Env: env}
	if opt.Tags != "" {
		cfg.BuildFlags = []string{"-tags=" + opt.Tags}
	}
	targets, err := useTargets(modPath, use)
	if err != nil {
		return nil, err
	}
	if err := resolveShortNames(targets, modPath, cfg); err != nil {
		return nil, err
	}
	var paths []string
	for p := range targets {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	pkgs, err := packages.Load(cfg, paths...)
	if err != nil {
		return nil, explain(err)
	}
	byPath := map[string]*packages.Package{}
	for _, p := range pkgs {
		if len(p.Errors) > 0 || p.Types == nil {
			hint := ""
			if p.PkgPath == modPath && len(use) == 0 {
				hint = fmt.Sprintf(" (if %s has no root package, say what you'd call: -use pkg.Func)", modPath)
			}
			return nil, fmt.Errorf("loading %s: %v%s", p.PkgPath, packageErr(p), hint)
		}
		byPath[p.PkgPath] = p
	}
	// "semver.Compare" may name a package by its last element; resolve now
	// that we know which packages exist.
	res := &TryResult{SchemaVersion: SchemaVersion, Module: modPath}
	src, used, skipped := callsFor(targets, byPath)
	res.Use, res.Skipped = used, skipped
	if len(used) == 0 {
		return nil, fmt.Errorf("nothing to call in %s: %s", modPath, strings.Join(skipped, "; "))
	}

	// Pass 2: weigh a program that makes exactly those calls.
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(src), 0o644); err != nil {
		return nil, err
	}
	if err := gocmd("mod", "tidy"); err != nil {
		return nil, err
	}
	topt := opt
	topt.Dir, topt.Patterns = tmp, []string{"."}
	rep, err := Analyze(topt)
	if err != nil {
		return nil, fmt.Errorf("weighing the trial program: %w", err)
	}
	m := rep.byPath[modPath]
	if m == nil {
		return nil, fmt.Errorf("%s is not in the trial build", modPath)
	}
	res.Version = m.Version
	if res.Dep = rep.Dep(m); res.Dep == nil {
		return nil, fmt.Errorf("%s is not a direct dependency of the trial program", modPath)
	}

	// Compare with the program you have, if there is one.
	if have, err := buildModules(opt); err == nil {
		res.Compared = true
		for _, path := range res.Dep.Drops {
			if have[path] {
				res.AlreadyInBuild = append(res.AlreadyInBuild, path)
				continue
			}
			res.NewModules = append(res.NewModules, path)
			res.NewLines += rep.byPath[path].Lines
		}
	}
	if res.AlreadyInBuild == nil {
		res.AlreadyInBuild = []string{}
	}
	if res.NewModules == nil {
		res.NewModules = []string{}
	}
	return res, nil
}

// resolveTarget turns the user's argument into a module path and either a
// `go get` query or "dir:<abs path>" for a module on disk.
func resolveTarget(arg, base string) (modPath, query string, err error) {
	dir := arg
	if !filepath.IsAbs(dir) && base != "" {
		dir = filepath.Join(base, dir)
	}
	looksLocal := strings.HasPrefix(arg, ".") || filepath.IsAbs(arg)
	if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && looksLocal {
		path := modulePath(b)
		if err := checkPath(path); err != nil {
			return "", "", fmt.Errorf("%s/go.mod: %w", arg, err)
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", "", err
		}
		return path, "dir:" + abs, nil
	}
	if looksLocal {
		return "", "", fmt.Errorf("%s: no go.mod there", arg)
	}
	path, _, _ := strings.Cut(arg, "@")
	if err := checkPath(arg); err != nil {
		return "", "", err
	}
	if !strings.Contains(arg, "@") {
		arg += "@latest"
	}
	return path, arg, nil
}

// checkPath rejects strings that the go command would read as a flag, or
// that can't be a module path or query. Arguments can come from an agent
// over MCP, so this is a boundary.
func checkPath(s string) error {
	if s == "" {
		return errors.New("empty module path")
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("invalid module path %q: starts with -", s)
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("invalid module path %q", s)
		}
	}
	return nil
}

// modulePath returns the module line of a go.mod file. (x/mod/modfile does
// this properly, but heft keeps to one dependency.)
func modulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && f[0] == "module" {
			if p, err := strconv.Unquote(f[1]); err == nil {
				return p
			}
			return f[1]
		}
	}
	return ""
}

type useTarget struct {
	pkg  string // import path, or a last element to resolve
	name string // Func or Type.Method; "" = every exported func
}

// useTargets groups the requested calls by package path. Names that use a
// short package name are filed under every candidate path; callsFor drops
// the ones that don't exist.
func useTargets(modPath string, use []string) (map[string][]useTarget, error) {
	out := map[string][]useTarget{}
	if len(use) == 0 {
		out[modPath] = append(out[modPath], useTarget{pkg: modPath})
		return out, nil
	}
	for _, u := range use {
		pkg, name := modPath, u
		if i := strings.LastIndex(u, "/"); i >= 0 {
			rest := u[i+1:]
			dot := strings.Index(rest, ".")
			if dot < 0 {
				return nil, fmt.Errorf("-use %q: want pkg.Func", u)
			}
			pkg, name = u[:i+1+dot], rest[dot+1:]
			if err := checkPath(pkg); err != nil {
				return nil, fmt.Errorf("-use %q: %w", u, err)
			}
		} else if parts := strings.Split(u, "."); len(parts) == 3 || (len(parts) == 2 && isLower(parts[0])) {
			// semver.Compare or semver.Version.String: a package element.
			pkg, name = shortPrefix+parts[0], strings.Join(parts[1:], ".")
		}
		out[pkg] = append(out[pkg], useTarget{pkg: pkg, name: name})
	}
	return out, nil
}

// shortPrefix marks a package named by its last element only, until
// resolveShortNames finds it among the module's packages.
const shortPrefix = "short:"

func resolveShortNames(targets map[string][]useTarget, modPath string, cfg *packages.Config) error {
	var all []string
	for key, ts := range targets {
		elem, ok := strings.CutPrefix(key, shortPrefix)
		if !ok {
			continue
		}
		if all == nil {
			c := *cfg
			c.Mode = packages.NeedName
			pkgs, err := packages.Load(&c, modPath+"/...")
			if err != nil {
				return explain(err)
			}
			for _, p := range pkgs {
				all = append(all, p.PkgPath)
			}
		}
		var hits []string
		for _, p := range all {
			if lastElem(p) == elem {
				hits = append(hits, p)
			}
		}
		switch len(hits) {
		case 0:
			return fmt.Errorf("-use %s.…: %s has no package %q", elem, modPath, elem)
		case 1:
		default:
			return fmt.Errorf("-use %s.…: several packages are called %q (%s); use the full import path", elem, elem, strings.Join(hits, ", "))
		}
		if strings.Contains(hits[0]+"/", "/internal/") {
			return fmt.Errorf("-use %s.…: %s is internal to %s; you can't call it directly, name what calls it", elem, hits[0], modPath)
		}
		delete(targets, key)
		for i := range ts {
			ts[i].pkg = hits[0]
		}
		targets[hits[0]] = append(targets[hits[0]], ts...)
	}
	return nil
}

func isLower(s string) bool { return s != "" && strings.ToLower(s[:1]) == s[:1] }

func lastElem(path string) string {
	path = strings.TrimSuffix(path, "/")
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func packageErr(p *packages.Package) error {
	var msgs []string
	for _, e := range p.Errors {
		msgs = append(msgs, e.Msg)
	}
	if len(msgs) == 0 {
		return errors.New("no type information")
	}
	return errors.New(strings.Join(msgs, "; "))
}

// callsFor writes a main package that calls each target with zero values,
// behind a condition that never holds: RTA sees the calls, nothing runs.
func callsFor(targets map[string][]useTarget, pkgs map[string]*packages.Package) (src string, used, skipped []string) {
	imports := map[string]string{} // path → local name
	importName := func(p *types.Package) string {
		if n, ok := imports[p.Path()]; ok {
			return n
		}
		n := fmt.Sprintf("p%d", len(imports))
		imports[p.Path()] = n
		return n
	}
	var body strings.Builder

	var paths []string
	for p := range targets {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pkg := pkgs[path]
		for _, t := range targets[path] {
			if pkg == nil {
				skipped = append(skipped, t.name+": no package "+path)
				continue
			}
			var fns []*types.Func
			var label []string
			scope := pkg.Types.Scope()
			if t.name == "" {
				for _, n := range scope.Names() {
					if f, ok := scope.Lookup(n).(*types.Func); ok && f.Exported() {
						fns, label = append(fns, f), append(label, lastElem(path)+"."+n)
					}
				}
			} else if typ, meth, ok := strings.Cut(t.name, "."); ok {
				tn, _ := scope.Lookup(typ).(*types.TypeName)
				if tn == nil {
					skipped = append(skipped, t.name+": no type "+typ+" in "+path)
					continue
				}
				obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(tn.Type()), true, pkg.Types, meth)
				f, _ := obj.(*types.Func)
				if f == nil || !f.Exported() {
					skipped = append(skipped, t.name+": no exported method "+meth)
					continue
				}
				fns, label = append(fns, f), append(label, lastElem(path)+"."+t.name)
			} else {
				f, _ := scope.Lookup(t.name).(*types.Func)
				if f == nil || !f.Exported() {
					skipped = append(skipped, t.name+": no exported function in "+path)
					continue
				}
				fns, label = append(fns, f), append(label, lastElem(path)+"."+t.name)
			}
			for i, f := range fns {
				call, why := callExpr(f, importName)
				if why != "" {
					skipped = append(skipped, label[i]+": "+why)
					continue
				}
				fmt.Fprintf(&body, "\t\t%s\n", call)
				used = append(used, label[i])
			}
		}
	}

	var b strings.Builder
	b.WriteString("// Code generated by heft try. DO NOT EDIT.\n\npackage main\n\nimport (\n\t\"os\"\n")
	var ips []string
	for p := range imports {
		ips = append(ips, p)
	}
	sort.Strings(ips)
	for _, p := range ips {
		fmt.Fprintf(&b, "\t%s %s\n", imports[p], strconv.Quote(p))
	}
	b.WriteString(")\n\nfunc main() {\n\tif os.Getenv(\"HEFT_TRY_NEVER\") == \"never\" {\n")
	b.WriteString(body.String())
	b.WriteString("\t}\n}\n")
	return b.String(), used, skipped
}

// callExpr writes a call to f with zero-value arguments, or says why it
// can't (unexported or internal types it would have to name, type
// parameters it can't pick).
func callExpr(f *types.Func, importName func(*types.Package) string) (string, string) {
	sig := f.Type().(*types.Signature)
	if sig.TypeParams().Len() > 0 || (sig.Recv() != nil && sig.RecvTypeParams().Len() > 0) {
		return "", "generic; name an instantiation by calling it from your own code instead"
	}
	problem := ""
	qual := func(p *types.Package) string {
		if strings.Contains(p.Path()+"/", "/internal/") {
			problem = "uses a type from internal package " + p.Path()
		}
		return importName(p)
	}
	typeStr := func(t types.Type) string {
		visitNamed(t, func(n *types.TypeName) {
			if n.Pkg() != nil && !n.Exported() {
				problem = "takes unexported type " + n.Name()
			}
		})
		return types.TypeString(t, qual)
	}
	var args []string
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if sig.Variadic() && i == params.Len()-1 {
			break
		}
		args = append(args, "*new("+typeStr(params.At(i).Type())+")")
	}
	var recv string
	if r := sig.Recv(); r != nil {
		t := r.Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		ts := typeStr(t)
		if types.IsInterface(t) {
			recv = "(*new(" + ts + "))."
		} else {
			recv = "new(" + ts + ")."
		}
	} else {
		recv = importName(f.Pkg()) + "."
	}
	if problem != "" {
		return "", problem
	}
	call := recv + f.Name() + "(" + strings.Join(args, ", ") + ")"
	if sig.Results().Len() > 0 {
		blanks := strings.TrimSuffix(strings.Repeat("_, ", sig.Results().Len()), ", ")
		call = blanks + " = " + call
	}
	return call, ""
}

// visitNamed calls fn for every named type spelled out in t.
func visitNamed(t types.Type, fn func(*types.TypeName)) {
	switch t := t.(type) {
	case *types.Named:
		fn(t.Obj())
		for i := 0; i < t.TypeArgs().Len(); i++ {
			visitNamed(t.TypeArgs().At(i), fn)
		}
	case *types.Alias:
		fn(t.Obj())
	case *types.Pointer:
		visitNamed(t.Elem(), fn)
	case *types.Slice:
		visitNamed(t.Elem(), fn)
	case *types.Array:
		visitNamed(t.Elem(), fn)
	case *types.Map:
		visitNamed(t.Key(), fn)
		visitNamed(t.Elem(), fn)
	case *types.Chan:
		visitNamed(t.Elem(), fn)
	case *types.Signature:
		for i := 0; i < t.Params().Len(); i++ {
			visitNamed(t.Params().At(i).Type(), fn)
		}
		for i := 0; i < t.Results().Len(); i++ {
			visitNamed(t.Results().At(i).Type(), fn)
		}
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			visitNamed(t.Field(i).Type(), fn)
		}
	}
}

// yourReplaces returns the replace directives of the module in opt.Dir, as
// go mod edit -replace arguments with local paths made absolute.
func yourReplaces(opt Options) []string {
	run := func(args ...string) []byte {
		cmd := exec.Command("go", args...)
		cmd.Dir, cmd.Env = opt.Dir, opt.Env()
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		return out
	}
	gomod := strings.TrimSpace(string(run("env", "GOMOD")))
	if gomod == "" || gomod == os.DevNull {
		return nil
	}
	var mf struct {
		Replace []struct {
			Old, New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(run("mod", "edit", "-json", gomod), &mf); err != nil {
		return nil
	}
	var out []string
	for _, r := range mf.Replace {
		old, repl := r.Old.Path, r.New.Path
		if r.Old.Version != "" {
			old += "@" + r.Old.Version
		}
		if r.New.Version != "" {
			repl += "@" + r.New.Version
		} else if !filepath.IsAbs(repl) {
			repl = filepath.Join(filepath.Dir(gomod), repl)
		}
		out = append(out, old+"="+repl)
	}
	return out
}

// buildModules lists the modules in the build of the program in opt.Dir.
func buildModules(opt Options) (map[string]bool, error) {
	patterns := opt.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	args := []string{"list", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}"}
	if opt.Tags != "" {
		args = append(args, "-tags="+opt.Tags)
	}
	args = append(args, "--")
	cmd := exec.Command("go", append(args, patterns...)...)
	cmd.Dir, cmd.Env = opt.Dir, opt.Env()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, l := range strings.Fields(string(out)) {
		have[l] = true
	}
	return have, nil
}
