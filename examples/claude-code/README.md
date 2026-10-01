# heft in Claude Code

A coding agent will happily `go get` a module to save writing ten lines. These two
pieces let Claude check that, with numbers it can't work out by reading code: how much
of the module the program actually reaches, and what it drags in.

## 1. Give Claude the tools (MCP)

```sh
git clone https://github.com/rijuld/heft && cd heft && go install .
# once a release is tagged, pin it instead:  go install github.com/rijuld/heft@vX.Y.Z
claude mcp add heft -- heft mcp
```

Claude then has `heft_try` (weigh a module *before* adding it), `heft_weigh`,
`heft_why`, `heft_extract` and `heft_diff`. The server keeps heft's safe defaults: it
won't download Go toolchains, and a tool call can't turn that off. Start it with
`heft mcp -offline -cgo=false` to also forbid module downloads and the C toolchain.

## 2. Check every go.mod change (hook)

Copy [`heft-hook.sh`](heft-hook.sh) into your project (say `.claude/hooks/`), then add
to `.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit|Bash",
        "hooks": [{ "type": "command", "command": "\"$CLAUDE_PROJECT_DIR\"/.claude/hooks/heft-hook.sh" }]
      }
    ]
  }
}
```

After Claude edits `go.mod` or runs `go get` / `go mod tidy`, the hook runs
`heft -base HEAD -fail-on heavy,inline`. If the change brought in a heavy dependency or
an inline candidate, it exits 2 and Claude sees the report. Set `HEFT_FAIL_ON` to
change which verdicts count.
