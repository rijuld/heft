# Contributing to heft

Thanks for helping Go programs lose weight. ⚖️

## Found a verdict that looks wrong?

That's the most useful issue you can file. Please include:

1. The program (repo + commit) and the command you ran.
2. The line from `heft` and the output of `heft why <module>`.
3. What you expected, and why.

Remember that reachability is a conservative *upper bound* (see the README). A module
that looks over-reached is often behind an interface or reflection. That's still worth
reporting, because better explanations are part of the job.

## Codebase tour

| Path | What it does |
| --- | --- |
| `main.go` | Flags, the `why` subcommand, exit codes. |
| `render.go` | Terminal output (tabwriter + optional colour) and `why` JSON. |
| `internal/weigh/weigh.go` | Everything else: loading, SSA + RTA, attribution, removal cost, verdicts. |
| `internal/weigh/testdata/` | A fake multi-module world wired together with `replace` directives. |

The pipeline in `Analyze`:

1. `packages.Load` with `LoadAllSyntax | NeedModule`.
2. `ssautil.AllPackages(..., ssa.InstantiateGenerics)` → `prog.Build()`.
3. `rta.Analyze` from each main package's `init` and `main`.
4. Walk every `*ast.FuncDecl` in every loaded package, attribute it to its module, and
   mark it reached if its position is in RTA's reachable set.
5. For each direct dependency, `reach()` the import graph with only the main module's
   edges into it cut, and diff that against the full set.
6. `verdict()` labels it. All thresholds are constants at the top of that function.

## Tests

```sh
go vet ./...
go test ./...
```

The fixture needs **no network**: `testdata/app/go.mod` uses `replace` to point at
sibling directories. To add a scenario:

1. Create `internal/weigh/testdata/<name>/go.mod` and some Go files.
2. Add a `require` and a `replace` for it to `testdata/app/go.mod`, and use it from
   `testdata/app/main.go`.
3. Check `cd internal/weigh/testdata/app && go build .` still works.
4. Assert **exact** counts in `weigh_test.go`. The fixture is small enough to count
   by hand, and that's the point.

## Ground rules

- Keep the dependency list at one: `golang.org/x/tools`. A dependency-weighing tool
  should practise what it preaches.
- Verdicts must be **explainable in one sentence** and conservative. If a rule needs a
  paragraph to justify, it belongs in `heft why`'s output, not in a label.
- Run `gofmt` (CI checks it).
