// Command heft weighs a Go program's dependencies against what it actually uses.
//
//	heft [flags] [packages]          weigh every direct dependency
//	heft why <module> [packages]     show exactly what you use from one module
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/rijuld/heft/internal/weigh"
)

const usage = `heft: weigh your Go dependencies against what you actually use

USAGE
    heft [flags] [packages]           weigh the direct dependencies of a program
    heft why <module> [packages]      show what you use from one module, and where

    packages defaults to ./... (every main package in the module).

FLAGS
    -json        machine-readable output
    -all         also list indirect modules
    -tags list   build tags, as for go build
    -C dir       run in dir, as for go build
    -fail-on v   exit 1 if any direct dependency gets verdict v
                 (inline | heavy | init-only | no-calls | shared | light | keep)
    -no-color    plain output (also honours NO_COLOR)

VERDICTS
    ✂ inline     you call a few small functions and it brings nothing else along
    ⚠ heavy      it drags in several modules or a lot of code for little use
    ◌ init-only  nothing is called; only its init() runs
    ◌ no-calls   nothing is called; used for types or constants only
    ⇄ shared     other dependencies need it too; dropping your import frees nothing
    ~ light      you reach under 10% of its code
    ✓ keep       earns its place

heft analyzes programs (main packages), using Rapid Type Analysis from
main.init and main.main. Library mode is on the roadmap.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("heft", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	jsonOut := fs.Bool("json", false, "")
	all := fs.Bool("all", false, "")
	tags := fs.String("tags", "", "")
	failOn := fs.String("fail-on", "", "")
	noColor := fs.Bool("no-color", false, "")
	dir := fs.String("C", "", "")

	parse := func(args []string) bool {
		if err := fs.Parse(args); err != nil {
			return false
		}
		return true
	}
	if !parse(args) {
		return 2
	}
	why := ""
	if fs.Arg(0) == "why" {
		if fs.NArg() < 2 {
			fmt.Fprintln(stderr, "heft why: which module? e.g. heft why github.com/spf13/cobra")
			return 2
		}
		why = fs.Arg(1)
		if !parse(fs.Args()[2:]) { // flags may also follow the module
			return 2
		}
	}

	if *failOn != "" && !slices.Contains(weigh.Verdicts, *failOn) {
		fmt.Fprintf(stderr, "heft: unknown verdict %q for -fail-on (want one of %s)\n", *failOn, strings.Join(weigh.Verdicts, ", "))
		return 2
	}

	fmt.Fprintln(stderr, "heft: loading and analyzing (this compiles type info for every dependency)…")
	rep, err := weigh.Analyze(weigh.Options{Dir: *dir, Patterns: fs.Args(), Tags: *tags})
	if err != nil {
		fmt.Fprintf(stderr, "heft: %v\n", err)
		return 2
	}

	p := painter(!*noColor && os.Getenv("NO_COLOR") == "" && isTerminal(stdout))

	if why != "" {
		m, err := rep.Lookup(why)
		if err != nil {
			fmt.Fprintf(stderr, "heft: %v\n", err)
			return 2
		}
		if *jsonOut {
			return writeJSON(stdout, whyJSON(rep, m))
		}
		renderWhy(stdout, p, rep, m)
		return 0
	}

	if *jsonOut {
		if code := writeJSON(stdout, rep); code != 0 {
			return code
		}
	} else {
		renderReport(stdout, p, rep, *all)
	}

	if *failOn != "" {
		for _, d := range rep.Direct {
			if d.Verdict == *failOn {
				fmt.Fprintf(stderr, "heft: %s is %q: %s\n", d.Path, d.Verdict, d.Note)
				return 1
			}
		}
	}
	return 0
}

func writeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "heft: %v\n", err)
		return 2
	}
	return 0
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
