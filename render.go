package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/rijuld/heft/internal/weigh"
)

type painter bool

func (p painter) wrap(code, s string) string {
	if !p {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (p painter) bold(s string) string { return p.wrap("1", s) }
func (p painter) dim(s string) string  { return p.wrap("2", s) }

var verdictStyle = map[string]struct{ icon, label, color string }{
	weigh.VerdictInline:   {"✂", "inline candidate", "1;36"},
	weigh.VerdictHeavy:    {"⚠", "heavy for what you use", "1;33"},
	weigh.VerdictInitOnly: {"◌", "only init() runs", "35"},
	weigh.VerdictNoCalls:  {"◌", "types/consts only", "35"},
	weigh.VerdictShared:   {"⇄", "needed by other deps too", "2"},
	weigh.VerdictLight:    {"~", "light use", "2"},
	weigh.VerdictKeep:     {"✓", "earns its keep", "32"},
}

func (p painter) verdict(v string) string {
	s := verdictStyle[v]
	return p.wrap(s.color, s.icon+" "+s.label)
}

func pct(part, whole int) string {
	if whole == 0 {
		return "–"
	}
	v := 100 * float64(part) / float64(whole)
	if v > 0 && v < 1 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", v)
}

func modLabel(m *weigh.Module) string {
	s := m.Path
	if m.Version != "" {
		s += " " + m.Version
	}
	return s
}

func renderReport(w io.Writer, p painter, r *weigh.Report, all bool) {
	progs := strings.Join(r.Programs, ", ")
	fmt.Fprintf(w, "\n⚖️  %s  %s\n\n", p.bold("heft"), p.dim(fmt.Sprintf("%s · %d program%s: %s", r.MainModule, len(r.Programs), plural(len(r.Programs)), progs)))

	if len(r.Modules) == 0 {
		fmt.Fprintln(w, "   No third-party modules. Nothing to weigh. 🪶")
		return
	}
	fmt.Fprintf(w, "   Third-party Go compiled in   %s lines across %d module%s\n", p.bold(weigh.Human(r.ThirdParty)), len(r.Modules), plural(len(r.Modules)))
	fmt.Fprintf(w, "   Reachable from main          %s of %s lines inside functions (%s)\n\n",
		p.bold(weigh.Human(r.ThirdReached)), weigh.Human(r.ThirdFuncLines), pct(r.ThirdReached, r.ThirdFuncLines))

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "   "+p.dim("DIRECT DEPENDENCY")+"\t"+p.dim("YOU CALL")+"\t"+p.dim("YOU REACH")+"\t"+p.dim("REMOVING IT DROPS")+"\t"+p.dim("VERDICT"))
	for _, d := range r.Direct {
		drops := fmt.Sprintf("%d module%s · %s lines", len(d.Drops), plural(len(d.Drops)), weigh.Human(d.DropLines))
		fmt.Fprintf(tw, "   %s\t%d of %d funcs\t%s line%s (%s)\t%s\t%s\n",
			modLabel(d.Module), d.ReachedFuncs, d.Funcs, weigh.Human(d.ReachedLines), plural(d.ReachedLines), pct(d.ReachedLines, d.FuncLines), drops, p.verdict(d.Verdict))
	}
	tw.Flush()

	var notes []*weigh.Dep
	for _, d := range r.Direct {
		if d.Verdict != weigh.VerdictKeep && d.Verdict != weigh.VerdictLight && d.Verdict != weigh.VerdictShared {
			notes = append(notes, d)
		}
	}
	if len(notes) > 0 {
		fmt.Fprintln(w)
		for _, d := range notes {
			st := verdictStyle[d.Verdict]
			fmt.Fprintf(w, "   %s %s: %s\n", p.wrap(st.color, st.icon), p.bold(d.Path), d.Note)
			if len(d.Drops) > 1 {
				fmt.Fprintf(w, "     %s\n", p.dim("drops with it: "+strings.Join(d.Drops[1:], ", ")))
			}
		}
	}

	if all {
		fmt.Fprintf(w, "\n   %s\n", p.dim("ALL MODULES"))
		tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
		for _, m := range r.Modules {
			kind := "indirect"
			if m.Direct {
				kind = "direct"
			}
			fmt.Fprintf(tw, "   %s\t%s\t%d pkgs\t%s lines\t%d of %d funcs reached\n", modLabel(m), kind, m.Packages, weigh.Human(m.Lines), m.ReachedFuncs, m.Funcs)
		}
		tw.Flush()
	}

	fmt.Fprintf(w, "\n   %s\n", p.dim("See exactly what you use:  heft why <module>"))
	fmt.Fprintf(w, "   %s\n\n", p.dim("Line counts are Go source in compiled packages; reachability is RTA from main (static, conservative)."))
}

func renderWhy(w io.Writer, p painter, r *weigh.Report, m *weigh.Module) {
	kind := "indirect dependency"
	if m.Direct {
		kind = "direct dependency"
	}
	fmt.Fprintf(w, "\n⚖️  %s  %s\n", p.bold(modLabel(m)), p.dim(kind))
	if m.Replace != "" {
		fmt.Fprintf(w, "    %s\n", p.dim("replaced by "+m.Replace))
	}
	fmt.Fprintf(w, "\n   You reach %s of %d functions · %s of %s function lines (%s) · %d package%s compiled in\n",
		p.bold(fmt.Sprint(m.ReachedFuncs)), m.Funcs, p.bold(weigh.Human(m.ReachedLines)), weigh.Human(m.FuncLines), pct(m.ReachedLines, m.FuncLines), m.Packages, plural(m.Packages))
	if m.InitFuncs > 0 {
		fmt.Fprintf(w, "   %s\n", p.dim(fmt.Sprintf("plus %d init() function%s that always run", m.InitFuncs, plural(m.InitFuncs))))
	}

	entries := r.Entries(m)
	fmt.Fprintf(w, "\n   %s\n", p.bold("WHERE THE REST OF THE PROGRAM CALLS IN"))
	if len(entries) == 0 {
		fmt.Fprintf(w, "     %s\n", p.dim("(no direct calls: only types, constants, init, or calls through interfaces/func values)"))
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	const maxEntries = 25
	for i, e := range entries {
		if i == maxEntries {
			fmt.Fprintf(tw, "     %s\n", p.dim(fmt.Sprintf("… and %d more", len(entries)-maxEntries)))
			break
		}
		fmt.Fprintf(tw, "     %s\t← %s\t%s\n", e.Callee, e.Caller, p.dim(fmt.Sprintf("%s:%d", filepath.Base(e.File), e.Line)))
	}
	tw.Flush()

	fmt.Fprintf(w, "\n   %s\n", p.bold("WHAT RUNS INSIDE IT"))
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	shown := 0
	for _, f := range m.Functions() {
		if !f.Reached || f.Init {
			continue
		}
		if shown == 30 {
			fmt.Fprintf(tw, "     %s\n", p.dim(fmt.Sprintf("… and %d more", m.ReachedFuncs-shown)))
			break
		}
		shown++
		fmt.Fprintf(tw, "     %s.%s\t%d line%s\t%s\n", filepath.Base(f.Package), f.Name, f.Lines, plural(f.Lines), p.dim(fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)))
	}
	if shown == 0 {
		fmt.Fprintf(tw, "     %s\n", p.dim("(nothing)"))
	}
	tw.Flush()

	if d := r.Dep(m); d != nil {
		fmt.Fprintf(w, "\n   %s\n", p.bold("REMOVING IT WOULD DROP"))
		fmt.Fprintf(w, "     %d module%s, %s lines of Go: %s\n", len(d.Drops), plural(len(d.Drops)), weigh.Human(d.DropLines), strings.Join(d.Drops, ", "))
		fmt.Fprintf(w, "\n   %s %s\n", p.verdict(d.Verdict), p.dim("· "+d.Note))
	}
	fmt.Fprintln(w)
}

type whyOut struct {
	Module    *weigh.Module `json:"module"`
	Dep       *weigh.Dep    `json:"dependency,omitempty"`
	Entries   []weigh.Entry `json:"entries"`
	Functions []*weigh.Func `json:"functions"`
}

func whyJSON(r *weigh.Report, m *weigh.Module) whyOut {
	return whyOut{Module: m, Dep: r.Dep(m), Entries: r.Entries(m), Functions: m.Functions()}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
