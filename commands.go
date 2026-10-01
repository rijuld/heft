package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/rijuld/heft/internal/weigh"
)

// cmdTry weighs a module before it's added.
func cmdTry(c *config, module string, extra []string) int {
	if len(extra) > 0 {
		fmt.Fprintf(c.stderr, "heft try: unexpected arguments %q (name what you'd call with -use)\n", extra)
		return 2
	}
	fmt.Fprintf(c.stderr, "heft: trying %s in a scratch module (your go.mod is left alone)…\n", module)
	res, err := weigh.Try(module, c.use, c.opts)
	if err != nil {
		fmt.Fprintf(c.stderr, "heft: %v\n", err)
		return 2
	}
	switch c.format {
	case "json":
		if code := writeJSON(c.stdout, c.stderr, res); code != 0 {
			return code
		}
	case "md":
		renderTryMarkdown(c.stdout, res)
	default:
		renderTry(c.stdout, c.painter(), res)
	}
	return c.gate([]*weigh.Dep{res.Dep})
}

func renderTry(w io.Writer, p painter, r *weigh.TryResult) {
	d := r.Dep
	fmt.Fprintf(w, "\n⚖️  %s  %s\n\n", p.bold("heft try"), p.dim(r.Module+" "+r.Version))
	fmt.Fprintf(w, "   Calling      %s\n", strings.Join(r.Use, ", "))
	fmt.Fprintf(w, "   You'd reach  %s of %d funcs · %s of %s function lines (%s)\n",
		p.bold(fmt.Sprint(d.ReachedFuncs)), d.Funcs, p.bold(weigh.Human(d.ReachedLines)), weigh.Human(d.FuncLines), pct(d.ReachedLines, d.FuncLines))
	fmt.Fprintf(w, "   It brings    %d module%s · %s lines of Go\n", len(d.Drops), plural(len(d.Drops)), weigh.Human(d.DropLines))
	if r.Compared {
		fmt.Fprintf(w, "   New to you   %d module%s · %s lines", len(r.NewModules), plural(len(r.NewModules)), weigh.Human(r.NewLines))
		if len(r.AlreadyInBuild) > 0 {
			fmt.Fprintf(w, "  %s", p.dim("(already in your build: "+strings.Join(r.AlreadyInBuild, ", ")+")"))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "   License      %s\n", licenseLabel(d.Module))
	if len(d.Drops) > 1 {
		fmt.Fprintf(w, "   %s\n", p.dim("brings along: "+strings.Join(d.Drops[1:], ", ")))
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(w, "   %s\n", p.dim("skipped "+s))
	}
	fmt.Fprintf(w, "\n   %s %s\n\n", p.verdict(d.Verdict), p.dim("· "+d.Note))
}

func renderTryMarkdown(w io.Writer, r *weigh.TryResult) {
	d := r.Dep
	st := verdictStyle[d.Verdict]
	fmt.Fprintf(w, "### ⚖️ heft try: `%s` %s\n\n", r.Module, r.Version)
	fmt.Fprintf(w, "- **Calling:** %s\n", "`"+strings.Join(r.Use, "`, `")+"`")
	fmt.Fprintf(w, "- **You'd reach:** %d of %d funcs · %s of %s function lines (%s)\n", d.ReachedFuncs, d.Funcs, weigh.Human(d.ReachedLines), weigh.Human(d.FuncLines), pct(d.ReachedLines, d.FuncLines))
	fmt.Fprintf(w, "- **It brings:** %d module%s · %s lines of Go\n", len(d.Drops), plural(len(d.Drops)), weigh.Human(d.DropLines))
	if r.Compared {
		fmt.Fprintf(w, "- **New to your build:** %d module%s · %s lines\n", len(r.NewModules), plural(len(r.NewModules)), weigh.Human(r.NewLines))
	}
	fmt.Fprintf(w, "- **License:** %s\n\n%s **%s**: %s\n", mdEscape(licenseLabel(d.Module)), st.icon, st.label, mdEscape(d.Note))
}

// cmdDiff weighs the program at -base and now, and shows what changed.
func cmdDiff(c *config) int {
	fmt.Fprintf(c.stderr, "heft: weighing %s and the working tree…\n", c.base)
	base, err := weigh.AnalyzeAt(c.base, c.opts)
	if err != nil {
		fmt.Fprintf(c.stderr, "heft: %v\n", err)
		return 2
	}
	head, err := weigh.Analyze(c.opts)
	if err != nil {
		fmt.Fprintf(c.stderr, "heft: %v\n", err)
		return 2
	}
	d := weigh.Diff(base, head, c.base)
	switch c.format {
	case "json":
		if code := writeJSON(c.stdout, c.stderr, d); code != 0 {
			return code
		}
	case "md":
		renderDiffMarkdown(c.stdout, d)
	default:
		renderDiff(c.stdout, c.painter(), d)
	}
	// In diff mode, -fail-on looks only at what this change brought in.
	return c.gate(d.Flagged())
}

func signed(n int) string {
	if n > 0 {
		return "+" + weigh.Human(n)
	}
	if n < 0 {
		return "−" + weigh.Human(-n)
	}
	return "±0"
}

func renderDiff(w io.Writer, p painter, d *weigh.Delta) {
	fmt.Fprintf(w, "\n⚖️  %s  %s\n\n", p.bold("heft"), p.dim("changes since "+d.Base))
	fmt.Fprintf(w, "   Third-party Go compiled in   %s → %s lines (%s)\n", weigh.Human(d.BaseLines), weigh.Human(d.HeadLines), p.bold(signed(d.HeadLines-d.BaseLines)))
	if len(d.Added)+len(d.Removed)+len(d.NewDirect)+len(d.Changed)+len(d.RemovedDirect) == 0 {
		fmt.Fprintf(w, "\n   %s\n\n", p.dim("No dependency changes."))
		return
	}
	for _, m := range d.Added {
		fmt.Fprintf(w, "   %s %s  %s\n", p.wrap("33", "+"), modLabel(m), p.dim(weigh.Human(m.Lines)+" lines"))
	}
	for _, m := range d.Removed {
		fmt.Fprintf(w, "   %s %s  %s\n", p.wrap("32", "−"), modLabel(m), p.dim(weigh.Human(m.Lines)+" lines"))
	}
	if len(d.NewDirect) > 0 {
		fmt.Fprintf(w, "\n   %s\n", p.bold("NEW DIRECT DEPENDENCIES"))
		for _, dep := range d.NewDirect {
			fmt.Fprintf(w, "     %s  %s %s\n", modLabel(dep.Module), p.verdict(dep.Verdict), p.dim("· "+dep.Note))
		}
	}
	if len(d.Changed) > 0 {
		fmt.Fprintf(w, "\n   %s\n", p.bold("VERDICT CHANGED"))
		for _, ch := range d.Changed {
			fmt.Fprintf(w, "     %s  %s → %s %s\n", modLabel(ch.Module), ch.Was, p.verdict(ch.Verdict), p.dim("· "+ch.Note))
		}
	}
	if len(d.RemovedDirect) > 0 {
		fmt.Fprintf(w, "\n   %s %s\n", p.bold("NO LONGER DIRECT"), strings.Join(d.RemovedDirect, ", "))
	}
	fmt.Fprintln(w)
}

func renderDiffMarkdown(w io.Writer, d *weigh.Delta) {
	fmt.Fprintf(w, "### ⚖️ heft: dependency changes since `%s`\n\n", d.Base)
	fmt.Fprintf(w, "Third-party Go compiled in: %s → %s lines (**%s**)\n\n", weigh.Human(d.BaseLines), weigh.Human(d.HeadLines), signed(d.HeadLines-d.BaseLines))
	if len(d.Added)+len(d.Removed) > 0 {
		fmt.Fprintln(w, "| | Module | Lines |")
		fmt.Fprintln(w, "| --- | --- | --- |")
		for _, m := range d.Added {
			fmt.Fprintf(w, "| ➕ | `%s` | %s |\n", modLabel(m), weigh.Human(m.Lines))
		}
		for _, m := range d.Removed {
			fmt.Fprintf(w, "| ➖ | `%s` | %s |\n", modLabel(m), weigh.Human(m.Lines))
		}
		fmt.Fprintln(w)
	}
	for _, dep := range d.NewDirect {
		st := verdictStyle[dep.Verdict]
		fmt.Fprintf(w, "- New: **`%s`** %s %s: %s\n", dep.Path, st.icon, st.label, mdEscape(dep.Note))
	}
	for _, ch := range d.Changed {
		st := verdictStyle[ch.Verdict]
		fmt.Fprintf(w, "- Changed: **`%s`** %s → %s %s: %s\n", ch.Path, ch.Was, st.icon, st.label, mdEscape(ch.Note))
	}
	if len(d.Added)+len(d.Removed)+len(d.NewDirect)+len(d.Changed) == 0 {
		fmt.Fprintln(w, "No dependency changes.")
	}
}
