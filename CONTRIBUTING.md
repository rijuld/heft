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
| `main.go` | Flags (accepted anywhere), subcommands, `-fail-on`/`-allow`, exit codes. |
| `render.go` | Terminal and Markdown output, `why` JSON. |
| `commands.go` | `heft try` and `heft -base`, with their output. |
| `mcp.go` | `heft mcp`: hand-rolled MCP (JSON-RPC over stdio). |
| `internal/weigh/weigh.go` | Loading, SSA + RTA, attribution, removal cost, verdicts. |
| `internal/weigh/try.go` | Weighing a module in a scratch module, via generated zero-value calls. |
| `internal/weigh/diff.go` | `git archive` of a base revision, and the report diff. |
| `internal/weigh/extract.go` | The reachable declarations of a module, as source. |
| `internal/weigh/license.go` | Guessing a module's SPDX license from its license file. |
| `internal/weigh/testdata/` | A fake multi-module world wired together with `replace` directives. |
| `testdata/*.json` | Golden JSON output. |

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
go test . -run Golden -update   # after an intended change to the JSON
```

Tests need **no network**: fixtures use `replace` directives pointing at sibling
directories, `heft try` is tested against a local module, and `-base` against a git
repository the test creates. The main fixture is `testdata/app`. Edge cases have
their own programs, so the main fixture's exact counts never move:

| Fixture | Covers |
| --- | --- |
| `app` → `big`, `tiny`, `shared`, `sidefx`, `mega-tiny` (+ `huge`, `deep1`, `deep2`) | every verdict, transitive drops, shared deps |
| `edgeapp` → `gen`, `lit` | `//line` directives; `var f = func(…)` literals |
| `cgoapp` → `cg` | cgo glue isn't the module's code (skipped without cgo) |
| `ws/` | a `go.work` workspace sibling is a dependency |
| `newgo`, `pintool` | heft never downloads a toolchain |
| `lib` | libraries are rejected |

To add a scenario:

1. Create `internal/weigh/testdata/<name>/go.mod` and some Go files.
2. Use it from a fixture program: `app` only if it doesn't change existing counts,
   otherwise a new program next to `edgeapp`.
3. Check `go build .` still works in that program's directory.
4. Assert **exact** counts. The fixtures are small enough to count by hand, and
   that's the point. Make sure the test fails without your fix.

If the JSON changes, `TestGoldenJSON` fails. Renaming, removing or changing the
meaning of a field also needs `weigh.SchemaVersion` bumped; adding a field doesn't.

## Ground rules

- Keep the dependency list at one: `golang.org/x/tools`. A dependency-weighing tool
  should practise what it preaches.
- Verdicts must be **explainable in one sentence** and conservative. If a rule needs a
  paragraph to justify, it belongs in `heft why`'s output, not in a label.
- Run `gofmt` (CI checks it).
- heft runs the `go` command on code people don't trust. Keep its defaults safe (no
  toolchain downloads; see SECURITY.md), and never let an MCP tool call loosen them.
