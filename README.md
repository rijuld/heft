<div align="center">

# ⚖️ heft

**Weigh your Go dependencies against what you actually use.**

`heft` builds your program's call graph and tells you, for each dependency, how much of it
your program can actually reach, what it drags in, and whether you'd be better off
copying the 12 lines you use.

![license: MIT](https://img.shields.io/badge/license-MIT-blue)
![go: 1.26+](https://img.shields.io/badge/go-1.26%2B-00ADD8)

```sh
heft ./cmd/yourtool
```

</div>

---

## Why this exists

Every dependency is a small act of trust: in its code, its maintainers, their CI, and
everyone who can publish a release. The last few years have made the cost of that trust
concrete: hijacked maintainer accounts, typosquats, self-spreading package worms, and
a backdoor that took years to plant. Meanwhile, coding agents will happily add a
module to your `go.mod` to save themselves writing a ten-line helper.

Go's culture already says it best: *"A little copying is better than a little
dependency."* But you can't act on that without knowing **which** dependencies are
little. `go mod graph` tells you what you import. `heft` tells you what you **use**:

- **You call**: of the functions a module compiles into your binary, how many are
  reachable from `main`.
- **Removing it drops**: which modules and how many lines vanish from the build if you
  delete your imports of it. Modules that other dependencies still need are
  correctly kept.
- **Verdict**: a conservative, explainable label, and `heft why` shows the exact call
  sites behind it.

## What it looks like

This is real output for [Glow](https://github.com/charmbracelet/glow) (commit `6b365ee`,
2026-09-22), analyzed in 2.5 s. It's a well-curated program, and heft says so:

```text
⚖️  heft  charm.land/glow/v3 · 1 program: charm.land/glow/v3

   Third-party Go compiled in   226k lines across 64 modules
   Reachable from main          90k of 124k lines inside functions (73%)

   DIRECT DEPENDENCY                          YOU CALL           YOU REACH          REMOVING IT DROPS        VERDICT
   charm.land/glamour/v2 v2.0.1               81 of 99 funcs     1.9k lines (93%)   10 modules · 67k lines   ✓ earns its keep
   github.com/spf13/viper v1.21.0             87 of 247 funcs    1.1k lines (49%)   9 modules · 33k lines    ✓ earns its keep
   mvdan.cc/sh/v3 v3.13.1                     329 of 478 funcs   6.6k lines (73%)   1 module · 12k lines     ✓ earns its keep
   github.com/charmbracelet/log v0.4.2        36 of 103 funcs    674 lines (68%)    4 modules · 10k lines    ✓ earns its keep
   charm.land/bubbles/v2 v2.1.1               156 of 166 funcs   1.8k lines (97%)   1 module · 2.9k lines    ✓ earns its keep
   golang.org/x/term v0.43.0                  8 of 45 funcs      49 lines (5%)      1 module · 1.2k lines    ~ light use
   ...
   github.com/charmbracelet/x/editor v0.1.0   3 of 5 funcs       49 lines (83%)     1 module · 88 lines      ✂ inline candidate
   charm.land/bubbletea/v2 v2.0.8             132 of 164 funcs   2.0k lines (93%)   0 modules · 0 lines      ⇄ needed by other deps too
   github.com/spf13/cobra v1.10.2             225 of 238 funcs   4.6k lines (98%)   0 modules · 0 lines      ⇄ needed by other deps too
   ...

   ✂ github.com/charmbracelet/x/editor: you use 49 lines of it; consider copying them (keep the license)
```

Then ask *why*:

```text
$ heft why editor

⚖️  github.com/charmbracelet/x/editor v0.1.0  direct dependency

   You reach 3 of 5 functions · 49 of 59 function lines (83%) · 1 package compiled in

   WHERE THE REST OF THE PROGRAM CALLS IN
     editor.Cmd         ← main.init      config_cmd.go:40
     editor.Cmd         ← ui.openEditor  editor.go:14
     editor.LineNumber  ← ui.openEditor  editor.go:14

   WHAT RUNS INSIDE IT
     editor.Cmd         22 lines  editor.go:56
     editor.LineNumber  17 lines  editor.go:25
     editor.getEditor   10 lines  editor.go:79

   REMOVING IT WOULD DROP
     1 module, 88 lines of Go: github.com/charmbracelet/x/editor

   ✂ inline candidate · you use 49 lines of it; consider copying them (keep the license)
```

And here's the synthetic fixture in [`internal/weigh/testdata`](internal/weigh/testdata),
built to show every verdict at once:

```text
   DIRECT DEPENDENCY              YOU CALL       YOU REACH       REMOVING IT DROPS      VERDICT
   example.com/big v0.0.0         2 of 8 funcs   4 lines (21%)   4 modules · 62 lines   ⚠ heavy for what you use
   example.com/tiny v0.0.0        1 of 3 funcs   6 lines (40%)   1 module · 23 lines    ✂ inline candidate
   example.com/sidefx v0.0.0      0 of 1 funcs   0 lines (0%)    1 module · 9 lines     ◌ only init() runs
   example.com/mega-tiny v0.0.0   1 of 1 funcs   1 line (100%)   1 module · 5 lines     ✂ inline candidate
   example.com/shared v0.0.0      1 of 2 funcs   1 line (50%)    0 modules · 0 lines    ⇄ needed by other deps too

   ⚠ example.com/big: brings 4 modules / 62 lines; you reach 5 of their 27 function lines (19%)
     drops with it: example.com/deep1, example.com/deep2, example.com/huge
```

## Verdicts

| | Verdict | Means | Rule |
| --- | --- | --- | --- |
| ✂ | `inline` | You call a few small functions and it brings nothing else along. | ≤ 3 reachable funcs, ≤ 60 reachable lines, drops only itself |
| ⚠ | `heavy` | It drags in a lot, and you use little of what it drags in. | drops ≥ 4 modules or ≥ 20k lines, **and** you reach < 25% of their function lines |
| ◌ | `init-only` | Nothing is called; only its `init()` runs (a blank import for side effects). | 0 reachable funcs, has `init` |
| ◌ | `no-calls` | Nothing is called; you use its types or constants only. | 0 reachable funcs |
| ⇄ | `shared` | Other dependencies import it too, so dropping *your* import frees nothing. | drops 0 modules |
| ~ | `light` | You reach under 10% of its code. | |
| ✓ | `keep` | It earns its place. | |

The thresholds are deliberately conservative and live in one place
([`verdict()`](internal/weigh/weigh.go)). A verdict is a prompt to look, not an order.

## Install

```sh
git clone https://github.com/rijuld/heft && cd heft && go install .
# once published:  go install github.com/rijuld/heft@latest
```

Requires Go 1.26+. The only dependency is `golang.org/x/tools`.

## Usage

```sh
heft                          # every main package under ./...
heft ./cmd/server             # one program
heft why cobra                # what you use from one module (path, suffix or substring)
heft -all                     # also list indirect modules
heft -json > heft.json        # machine-readable (report, or `why` details)
heft -tags netgo,osusergo     # build tags, as for go build
heft -C ../other/repo         # run in another directory
```

### In CI: stop new heavyweights at the door

```yaml
- run: go install github.com/rijuld/heft@latest
- run: heft -fail-on heavy ./cmd/yourtool   # unknown verdict names exit 2, so typos can't silently pass
```

Exit codes: `0` ok · `1` a `-fail-on` verdict matched · `2` load or usage error.

## How it works

```
 go/packages ──► SSA (x/tools) ──► RTA call graph from main.init + main.main
      │                                     │
      │   every func decl, attributed       │   reachable functions, matched back to
      │   to its module, with line spans    │   source declarations by position
      ▼                                     ▼
 package import graph ──► for each direct dep D: walk from main, cutting only the
                          edges *from your module* into D ──► what disappears
```

1. **Load** the program with `go/packages` (full syntax and types, with module info).
2. **Build SSA** with generics instantiated and run **Rapid Type Analysis** from each
   main package's `init` and `main`. This is the same setup as
   [`golang.org/x/tools/cmd/deadcode`](https://pkg.go.dev/golang.org/x/tools/cmd/deadcode).
3. **Attribute** every source-level function and method to the module that declares it,
   matching reachability by source position, so generic instances and wrappers fold
   onto their declaration.
4. **Removal cost**: for each direct dependency, walk the package import graph from
   `main` with only *your* edges into it cut. Whatever becomes unreachable is what that
   dependency costs you. Other dependencies that also import it keep it alive, which
   is where `⇄ shared` comes from.

heft practises what it preaches. Its only dependency is `golang.org/x/tools`, and
`heft .` on itself reports `830 of 1262 funcs · 16k lines (66%) · ✓ earns its keep`.

### What the numbers mean (and don't)

- **"Reachable" is an upper bound.** RTA is sound but conservative. Interface calls,
  reflection and `init` chains make more code look reachable than your program really
  runs. When heft says you reach 5%, you *really* use at most 5%. That's why the
  `inline` and `heavy` verdicts are safe to act on, while `keep` is generous.
- **Lines** are Go source lines in the packages that are compiled in, not the whole
  module. Unused packages of a module don't count against it.
- **Only programs.** heft analyzes `main` packages. Libraries have no single entry
  point; library mode is on the roadmap.
- Tests and test-only dependencies are excluded: heft weighs what ships.

## Prior art

- [`deadcode`](https://pkg.go.dev/golang.org/x/tools/cmd/deadcode) reports unreachable
  functions in *your* module. heft uses the same analysis to answer a different
  question, per dependency.
- [`depscheck`](https://github.com/divan/depscheck) (2016) counted per-package usage and
  suggested removing tiny imports. It predates Go modules and doesn't measure transitive
  cost.
- [`goda`](https://github.com/loov/goda) and `go mod graph` explore the dependency
  *graph*. heft adds *usage*.

## Contributing

Issues and PRs are welcome. The best contributions are **real programs where a verdict
looks wrong**; please include the module and `heft why` output. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the codebase tour and how the offline test
fixtures work.

### Roadmap

Good first issues are marked 🌱.

- 🌱 `-sort` flag (by drops, by reach, by name)
- 🌱 Markdown output for PR comments (`-format md`)
- 🌱 Show each module's license in `heft why` (it matters when you inline)
- **Library mode:** roots = exported API + tests
- **Diff mode:** `heft -base main` shows what a PR added to the build and how much of it
  is used
- Binary-size attribution alongside lines (from the linker's symbol table)
- Count `go:embed` payloads and cgo in a module's weight

## License

MIT. See [LICENSE](LICENSE).
