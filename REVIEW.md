# heft review: what's missing, what's broken, what makes it worth having

Reviewed at commit `178e49f` on 2026-10-01 with Go 1.27.1 (darwin/arm64).
`go vet` and `go test ./...` pass, and CI's dogfood step passes (`heft .` → `830 of 1262
funcs · 16k lines (66%) · ✓ earns its keep`).

Every finding marked **verified** below has a repro that was run. **Unverified** means
it was reasoned from the code but not exercised.

## Status

Addressed on the `fix/review-findings` branch. The correctness fixes (§2.1–2.3) and
the MCP argument checks have tests confirmed to fail on the old code; the CLI and
security changes are covered by new tests. CI passes on Linux, macOS and Windows,
plus govulncheck.

| § | Item | Status |
| --- | --- | --- |
| 1 | `heft try` (pre-adoption check) | ✅ Fixed. Generates real zero-value calls, because RTA doesn't count functions only taken as values (verified). Never edits go.mod/go.sum (tested). |
| 1 | Diff mode | ✅ Fixed: `heft -base <ref>`, via `git archive`; `-fail-on` then gates only new/changed deps |
| 1 | Inline extraction | ✅ Fixed: `heft extract`. Tested to `go build` for three fixtures; Glow's `editor` builds too. |
| 1 | License per module | ✅ Fixed: SPDX guess in `why`, JSON and Markdown, and in the `inline` note |
| 1 | Versioned JSON | ✅ Fixed: `schema_version` + golden tests |
| 1 | Integrations | ✅ Fixed: `heft mcp`, a Claude Code hook (tested end to end), a composite GitHub Action |
| 1 | Markdown output | ✅ Fixed: `-format md` |
| 1 | Library mode | ⏸ Deferred (roadmap) |
| 2.1 | `//line` negative counts | ✅ Fixed |
| 2.2 | cgo glue counted | ✅ Fixed. Glue is filtered only in packages cgo rewrote, so goyacc-style code keeps its functions. |
| 2.3 | Flags after packages | ✅ Fixed (`--` ends flags) |
| 2.3 | `-fail-on` single value | ✅ Fixed: list, names every offender, plus `-allow` |
| 2.3 | `why ""` | ✅ Fixed (exit 2) |
| 2.3 | `writeJSON` stderr | ✅ Fixed |
| 2.3 | Test message | ✅ Fixed |
| 2.3 | Func literals in `var` | ✅ Verified (reported as "types/consts only"), then fixed |
| 2.3 | `go.work` | ✅ Verified (reported "no third-party modules"), then fixed: your code = modules of the weighed programs |
| 3.1 | Toolchain downloads | ✅ Fixed: `GOTOOLCHAIN=local` by default, `-toolchain=auto` to opt out |
| 3.2 | Module downloads | ✅ `-offline`. `-mod=readonly` is already the go command's default, and forcing it would break vendoring. |
| 3.3 | cgo runs the C toolchain | ✅ `-cgo=false` |
| 3.4 | Parent `go.work` | ✅ Documented in SECURITY.md (`GOWORK=off`); honoured by default, like `go build` |
| 3.5 | CI hygiene | ✅ Fixed: explicit `permissions`, SHA-pinned actions, no persisted credentials, Windows, govulncheck. govulncheck found stdlib vulnerabilities reachable from heft under Go 1.26.0, so CI and releases now use patched Go. |
| 3.6 | Distribution | ✅ GoReleaser + provenance workflow, run on tag push. README no longer recommends `@latest` in CI. **No tag has been pushed yet.** |
| 3.7 | SECURITY.md | ✅ Added |
| new | Agent-supplied MCP arguments reaching `go`/`git` as flags | ✅ Found while fixing (`heft_try` with `module: "-modfile=…"` reached `go get` as a flag), fixed and tested: paths and refs starting with `-` are refused, `--`/`--end-of-options` everywhere, `git archive` gets only the resolved commit SHA |
| 4 | Memory | 📝 Documented. Reducing it is deferred. |
| 4 | Progress / `-v` | ✅ Fixed |
| 4 | `-sort` | ✅ Fixed |
| 4 | `-version` | ✅ Fixed |
| 4 | Exit codes | ✅ Documented in `-h` and README |
| 4 | Configurable thresholds | ⏸ Deferred (roadmap); `-allow` covers the CI need |
| 4 | Allowlisting | ✅ `-allow` |

---

## 1. Why heft is worth existing in an agent world (and what's missing for it)

A coding agent like Claude can read your `go.mod`, grep for imports, and guess.
It **cannot** do what heft does:

- **Whole-program reachability.** Answering "which of x/tools' 1,262 functions does
  this binary reach?" requires building SSA and running RTA over ~57k lines of
  third-party code. No amount of reading files gets that right. Interface dispatch,
  generics instantiation and reflection all decide the answer.
- **Removal cost with shared-dependency awareness.** "If I drop `big`, do `deep1`,
  `deep2`, `huge` go too, but `shared` stays?" is a graph cut over the package import
  graph. An agent guessing from `go.mod` gets this wrong whenever two dependencies
  share a transitive.
- **Reproducibility.** The same input gives byte-identical JSON (**verified**: 6 runs,
  1 hash). An agent's judgement doesn't, so heft can serve as a CI gate and an agent's
  judgement can't.

So heft's real audience is as much **agents** as humans: it's the oracle an agent
should call before and after touching `go.mod`. Today it's built for humans reading
a terminal. Here is what would make it a tool agents *need*:

| Gap | Why it matters for agents | Suggested shape |
| --- | --- | --- |
| **Pre-adoption check** | Agents add deps at `go get` time; heft only weighs what's already in the build, i.e. after the damage is done. | `heft try <module>[@version] [-use pkg.Func,...]`: load the candidate in a temp overlay and report what it would drag in. |
| **Diff mode** (on roadmap) | "What did this agent's PR add to the build, and how much is used?" is exactly the review question. | `heft -base origin/main`, which weighs both and prints only the deltas. |
| **Inline extraction** | The `✂ inline` note says "consider copying them". An agent can't reliably find the reachable *closure* (helpers, unexported types, consts) by reading. | `heft extract <module>`: emit the reachable closure as one Go file, with the module's LICENSE header. heft already has the reached set. |
| **License per module** (on roadmap) | Gates whether inlining is allowed at all. | Detect `LICENSE*` in the module dir and include an SPDX guess in `why` and the JSON. |
| **Stable, versioned JSON** | Agents parse JSON. There's no `schema_version` and no golden test, so a field rename silently breaks consumers and nothing catches it. | Add `"schema_version": 1`, document the schema in the README, and add a golden-file test for `-json`. |
| **Integrations** | Discovery. An agent won't run a tool it doesn't know exists. | A Claude Code hook example (`PostToolUse` on edits to `go.mod` → `heft -fail-on heavy`), a GitHub Action, and optionally a tiny MCP server wrapping `Analyze`/`why`. |
| **Markdown output** (on roadmap) | PR comments are where agents and reviewers meet. | `-format md`. |
| **Library mode** (on roadmap) | Most Go repos agents touch are libraries; heft currently exits 2 on them. | Roots = exported API (+ tests optionally). |

The strongest single addition is **`heft try`**. It turns heft from a post-hoc auditor
into the check an agent runs before it adds a dependency.

---

## 2. Correctness bugs

### 2.1 `//line` directives produce negative line counts and wrong verdicts (verified, high)

Generated code (goyacc, ragel, some code generators) uses `//line file:N` directives.
heft computes lines with `prog.Fset.Position`, which applies those directives, so a
function's "end line" can be smaller than its "start line".

Repro: a dependency containing

```go
//line grammar.y:500
func Big() int {
	x := 1
	// ...
//line grammar.y:10
	return x
}
```

gives:

```
"func_lines": -487, "reached_lines": -488, "verdict": "inline",
"note": "you use -488 lines of it; consider copying them (keep the license)"
```

The same adjusted positions are used as keys for the `reached` map. That's consistent
on both sides, but it also means `File` in output points to `grammar.y`, which doesn't
exist in the module.

**Fix:** compute line *spans* from unadjusted start and end positions
(`prog.Fset.PositionFor(pos, false)`), and keep the adjusted position for the file
name used in display and attribution (see §2.2 for why the adjusted name is the one
to keep). Clamp to ≥ 1 as a backstop. Add a fixture module with a `//line` directive.

### 2.2 cgo glue is counted as the module's own code (verified, medium)

For a package that uses cgo, `p.Syntax` holds the cgo-*generated* files. Their
synthetic functions (`_Cfunc_rand`, `_cgo_runtime_cgocall`, …) are counted as the
module's declarations, and their files show up with hash names.

Repro: a module with one 6-line function calling `C.rand()`:

```
You reach 3 of 9 functions · 11 of 17 function lines (65%)
  cg._Cfunc_rand           4 lines  fb90ce27…-d:47
  cg._cgo_runtime_cgocall  1 line   fb90ce27…-d:31
REMOVING IT WOULD DROP  1 module, 67 lines of Go   (the source file is 11 lines)
```

This inflates both the function counts and the "drops N lines" figures for anything
cgo-backed (sqlite drivers, etc.), which skews `heavy`/`inline`.

**Fix:** skip declarations whose **adjusted** file name isn't in `p.GoFiles`. cgo
rewrites the user's own files into the build cache too, and maps them back with
`//line` directives. Verified: `Rand`'s adjusted position is `cg.go:6`, but its
unadjusted file is a hash-named cache file, while the glue's adjusted file is also a
hash name. Filtering on the *unadjusted* name would therefore drop every function in
a cgo package, including the user's. Count file lines from the original `GoFiles`.

**§2.1 and §2.2 must be fixed together**, with one fixture that has both a cgo
package and a `//line` directive.
The README roadmap already mentions "cgo in a module's weight", and it can be counted
separately and explicitly.

### 2.3 Smaller issues (verified unless noted)

- **Flags after packages are rejected with a confusing error.** `heft . -json` →
  `malformed import path "-json": leading dash`. `why` already re-parses trailing
  flags; do the same for the main command (loop `fs.Parse` over remaining args, or
  error clearly with "flags must come before packages").
- **`-fail-on` takes one verdict.** `-fail-on heavy,inline` → "unknown verdict". CI
  users will want a list. It also stops at the first match, so only one offender is
  reported; report all of them, then exit 1.
- **`heft why ""`** silently prints the full report with exit 0 instead of an error.
  `why` is tracked as an empty string, so `""` is indistinguishable from "no `why`".
- **`writeJSON` writes errors to `os.Stderr`** instead of the injected `stderr`
  (`main.go`), so it bypasses the test harness.
- **Test message mismatch:** `TestIndirectModulesAreAttributed` checks `!= 13` but
  says `want 12`.
- **Funcs in package-level `var` initialisers** (`var X = func() {...}`) aren't
  `*ast.FuncDecl`, so their lines are never in `FuncLines`/`ReachedLines`.
  (Unverified; it follows from the `decl.(*ast.FuncDecl)` filter.)
- **`go.work` workspaces:** every workspace module has `Module.Main == true`, so a
  module that's a "dependency" in a workspace is treated as your own code. Also
  `r.MainModule` is overwritten by whichever main package comes last. (Unverified.)

### 2.4 The soundness claim holds where it was tested (verified)

The README says `inline`/`heavy` are "safe to act on" because RTA over-approximates.
A method reached *only* by name, via `text/template` `{{.FullName}}` and via
`reflect.Value.MethodByName`, was correctly reported as reached (`2 of 2 functions`).
Still worth adding this as a fixture so it stays true. `//go:linkname` pulls were
not tested.

---

## 3. Security

heft is an analysis tool, so people will run it on code they don't trust: a PR's
branch, a candidate dependency, or in CI on fork PRs. Today it isn't a read-only
operation.

1. **Running heft can download and execute a Go toolchain (verified).** heft calls
   `go list` via `go/packages`, which honours `toolchain` in the target's `go.mod`.
   A repo with `toolchain go1.99.9` makes heft print
   `go: downloading go1.99.9 (darwin/arm64)`. Toolchains are checksum-verified
   against sum.golang.org, so this isn't arbitrary code. It is still a network side
   effect and a different compiler than you think you're using, and an untrusted
   `go.mod` controls it. **Fix:** set `GOTOOLCHAIN=local` in `cfg.Env` by default,
   with a flag to opt out, and document it.
2. **It can download modules.** With a missing `go.sum` entry or `-mod=mod` in
   `GOFLAGS`, `go list` fetches from `GOPROXY`. Consider defaulting to
   `GOFLAGS=-mod=readonly` and offering `-offline` (`GOPROXY=off`).
3. **cgo runs the C toolchain.** `go list -compiled` invokes cgo, which runs the C
   compiler/preprocessor over the target's C code. `#cgo` flags are allowlisted, but
   this is still executing a compiler on untrusted input. Offer `-cgo=false`
   (`CGO_ENABLED=0`) and mention it in the docs.
4. **A parent `go.work` changes results.** Running `heft -C some/dir` inside a tree
   with a `go.work` silently uses the workspace. Consider `GOWORK=off` unless asked.
5. **CI hygiene** (`.github/workflows/ci.yml`):
   - Set `permissions: contents: read` explicitly, so the job doesn't depend on the
     repo or org default for `GITHUB_TOKEN`.
   - Pin `actions/checkout` and `actions/setup-go` to commit SHAs. A tool whose pitch
     is supply-chain caution should do it itself.
   - Add `govulncheck ./...` and a Windows runner (the output uses emoji and
     `ModeCharDevice` checks).
6. **Distribution.** The README tells users to put `go install …@latest` in CI.
   `@latest` is the unpinned-dependency pattern heft argues against. Tag releases
   (`v0.1.0`), recommend `@v0.1.0`, and consider GoReleaser with checksums and
   provenance (SLSA / `actions/attest-build-provenance`).
7. **No `SECURITY.md`.** Add one with a reporting address.

Not a concern: the JSON/text output only contains paths, names and counts from the
analysed program. There are no shell-outs besides `go list`, and nothing is written to disk except
the Go build/module caches.

---

## 4. Usability

- **Memory.** Peak RSS was ~600 MB weighing heft itself (57k third-party lines).
  `LoadAllSyntax` keeps syntax and types for everything, so big programs
  (Kubernetes-scale) will need several GB. Worth a note in the README, and later,
  dropping `Syntax` for std packages or loading deps with `NeedTypes` only where
  possible.
- **No progress for slow loads.** The single "loading and analyzing…" line gives no
  sense of progress on a 2-minute load. Print phase timings with `-v`.
- **No `-sort`** (on roadmap) and no way to filter to one verdict in text output.
- **No `-version` flag.** With `go install`, `debug.ReadBuildInfo` can provide it for
  free, and bug reports need it.
- **Exit code 2 conflates** "your code doesn't compile" with "bad flag". Fine for now,
  but document it.
- **Thresholds aren't configurable.** That's a deliberate choice, but teams will ask.
  A `-heavy-min-modules` style override, or a `.heft.toml`, can come later. Keep the
  defaults conservative.
- **Allowlisting.** CI users need "I know `big` is heavy, it's accepted": e.g.
  `-allow example.com/big` or a comment-annotated allowlist file. Without it,
  `-fail-on heavy` is all-or-nothing and will get turned off.

---

## 5. Suggested order

1. Fix §2.1 (`//line`) and §2.2 (cgo). They produce wrong verdicts on real programs.
   Add fixtures for both.
2. `GOTOOLCHAIN=local`, `GOWORK=off` and `-mod=readonly` defaults, plus CI
   `permissions`/SHA pinning (§3).
3. Tag `v0.1.0`, publish, and update the install docs to a pinned version.
4. `schema_version` + golden JSON test, multi-value `-fail-on`, `-allow`, `-version`.
5. `heft try` and diff mode. These make heft the tool an agent has to call.
6. Claude Code hook / GitHub Action / MCP examples in the README.
