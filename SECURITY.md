# Security

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting:
**Security → Report a vulnerability** on
[rijuld/heft](https://github.com/rijuld/heft/security/advisories/new).
Don't open a public issue. You'll get a reply within a week.

## What heft does to the code it weighs

People point heft at code they didn't write: a pull request, a dependency they're
evaluating, a repo they just cloned. heft never runs that code, but it does run the
`go` command on it, via `golang.org/x/tools/go/packages`. Here is what that means and
what heft does about it.

| The go command may… | heft's default | To change it |
| --- | --- | --- |
| switch to (and download) the toolchain the target's `go.mod` names | refused: `GOTOOLCHAIN=local` | `-toolchain=auto`, or set `GOTOOLCHAIN` yourself |
| download modules missing from the module cache | allowed, checksum-verified against `go.sum` and sum.golang.org | `-offline` (`GOPROXY=off`) |
| run the C toolchain over cgo packages (`#cgo` flags are allowlisted) | allowed, because cgo changes which files are compiled | `-cgo=false` (`CGO_ENABLED=0`) |
| use a `go.work` file in or above the directory | honoured, as `go build` would | `GOWORK=off heft …` |

`heft try` downloads the module you name, in a temporary directory; it never edits your
`go.mod` or `go.sum`.

`heft mcp` serves the same tools to agents. A tool call can't loosen these
defaults, and module paths, package paths and git refs that would reach the `go` or
`git` command as a flag are refused.

For the strictest run on untrusted code: `heft -offline -cgo=false`, inside a container
or VM if you're being thorough.

heft itself writes nothing to disk except the Go build and module caches (and
`heft -base` / `heft try` temporary directories, removed when it exits).
