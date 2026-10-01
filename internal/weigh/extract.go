package weigh

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Extract returns, for each of m's packages that the program reaches into,
// Go source holding the reachable functions and every package-level type,
// constant and variable they refer to, with the module's license on top.
// It's where copying starts: it compiles for self-contained packages, but
// references to the module's other packages (or anything heft can't see)
// are left for you to resolve.
func (r *Report) Extract(m *Module) (string, error) {
	var out strings.Builder
	var paths []string
	for _, p := range m.packages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	n := 0
	for _, path := range paths {
		src, ok, err := r.extractPackage(m, path)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		if n > 0 {
			out.WriteString("\n")
		}
		out.WriteString(src)
		n++
	}
	if n == 0 {
		return "", fmt.Errorf("nothing in %s is reachable from your program: there's nothing to copy", m.Path)
	}
	if n > 1 {
		return "// heft extract: " + strconv.Itoa(n) + " packages follow, each starting at its own \"package\" line; save each as its own file.\n\n" + out.String(), nil
	}
	return out.String(), nil
}

func (r *Report) extractPackage(m *Module, path string) (string, bool, error) {
	p := r.pkgs[path]
	if p == nil {
		return "", false, nil
	}
	if userFiles(p) != nil {
		return "", false, fmt.Errorf("%s uses cgo; heft extract doesn't copy cgo code", path)
	}

	// Package-level declarations by the objects they declare.
	type declRef struct {
		file *ast.File
		node ast.Node // *ast.FuncDecl, or the *ast.GenDecl/spec to print
		gen  *ast.GenDecl
	}
	byObj := map[types.Object]declRef{}
	var funcs []declRef
	reachedAt := map[token.Position]bool{}
	for _, fn := range m.functions {
		if fn.Package == path && fn.Reached && !fn.Init {
			reachedAt[fn.Pos] = true
		}
	}
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if reachedAt[r.fset.Position(d.Name.Pos())] {
					funcs = append(funcs, declRef{f, d, nil})
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					node := ast.Node(spec)
					if d.Tok == token.CONST && len(d.Specs) > 1 {
						node = d // iota: keep the group together
					}
					switch s := spec.(type) {
					case *ast.TypeSpec:
						byObj[p.TypesInfo.Defs[s.Name]] = declRef{f, node, d}
					case *ast.ValueSpec:
						for i, name := range s.Names {
							ref := declRef{f, node, d}
							if i < len(s.Values) {
								if lit, ok := s.Values[i].(*ast.FuncLit); ok && reachedAt[r.fset.Position(lit.Pos())] {
									funcs = append(funcs, ref)
								}
							}
							byObj[p.TypesInfo.Defs[name]] = ref
						}
					}
				}
			}
		}
	}
	if len(funcs) == 0 {
		return "", false, nil
	}

	// Follow references from what's included to other package-level
	// declarations, and collect the imports they use.
	included := map[ast.Node]bool{}
	var order []declRef
	imports := map[string]string{} // path → name used in the source
	var queue []declRef
	add := func(d declRef) {
		if !included[d.node] {
			included[d.node] = true
			order = append(order, d)
			queue = append(queue, d)
		}
	}
	for _, d := range funcs {
		add(d)
	}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		ast.Inspect(d.node, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			switch obj := p.TypesInfo.Uses[id].(type) {
			case *types.PkgName:
				imports[obj.Imported().Path()] = obj.Name()
			case nil:
			default:
				if obj.Pkg() == p.Types && obj.Parent() == p.Types.Scope() {
					if ref, ok := byObj[obj]; ok {
						add(ref)
					}
				}
			}
			return true
		})
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].node.Pos() < order[j].node.Pos() })

	var b strings.Builder
	fmt.Fprintf(&b, "// Copied from %s (%s %s) by heft extract.\n", path, m.Path, m.Version)
	fmt.Fprintf(&b, "// These are the declarations your program reaches; review before use.\n")
	b.WriteString(licenseComment(m))
	fmt.Fprintf(&b, "\npackage %s\n\n", p.Name)
	if len(imports) > 0 {
		var ips []string
		for ip := range imports {
			ips = append(ips, ip)
		}
		sort.Strings(ips)
		b.WriteString("import (\n")
		for _, ip := range ips {
			name := imports[ip]
			if name == lastElem(ip) {
				fmt.Fprintf(&b, "\t%s\n", strconv.Quote(ip))
			} else {
				fmt.Fprintf(&b, "\t%s %s\n", name, strconv.Quote(ip))
			}
		}
		b.WriteString(")\n\n")
	}
	srcCache := map[string][]byte{}
	text := func(f *ast.File, from, to token.Pos) (string, error) {
		name := r.fset.PositionFor(f.Pos(), false).Filename
		src, ok := srcCache[name]
		if !ok {
			var err error
			if src, err = os.ReadFile(name); err != nil {
				return "", err
			}
			srcCache[name] = src
		}
		tf := r.fset.File(f.Pos())
		return string(src[tf.Offset(from):tf.Offset(to)]), nil
	}
	for _, d := range order {
		from, to := d.node.Pos(), d.node.End()
		prefix := ""
		switch n := d.node.(type) {
		case *ast.FuncDecl:
			if n.Doc != nil {
				from = n.Doc.Pos()
			}
		case *ast.GenDecl:
			if n.Doc != nil {
				from = n.Doc.Pos()
			}
		case ast.Spec:
			prefix = d.gen.Tok.String() + " "
			if len(d.gen.Specs) == 1 && d.gen.Doc != nil {
				b.WriteString(mustText(text(d.file, d.gen.Doc.Pos(), d.gen.Doc.End())) + "\n")
			}
		}
		s, err := text(d.file, from, to)
		if err != nil {
			return "", false, err
		}
		b.WriteString(prefix + s + "\n\n")
	}
	out, err := format.Source([]byte(b.String()))
	if err != nil {
		return b.String(), true, nil // unformatted beats nothing
	}
	return string(out), true, nil
}

func mustText(s string, err error) string {
	if err != nil {
		return ""
	}
	return s
}

func licenseComment(m *Module) string {
	if m.LicenseFile == "" {
		return "//\n// heft found no license file for this module: check that you may copy it.\n"
	}
	b, err := os.ReadFile(m.LicenseFile)
	if err != nil {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "//\n// License (%s), from %s:\n//\n", licenseOrUnknown(m.License), lastElem(strings.ReplaceAll(m.LicenseFile, "\\", "/")))
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		out.WriteString(strings.TrimRight("//   "+line, " ") + "\n")
	}
	return out.String()
}

func licenseOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
