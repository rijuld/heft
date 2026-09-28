package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const app = "internal/weigh/testdata/app"

func runHeft(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestReportText(t *testing.T) {
	code, out, errs := runHeft("-C", app)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"example.com/tiny v0.0.0", "✂ inline candidate",
		"example.com/big v0.0.0", "⚠ heavy for what you use",
		"drops with it: example.com/deep1, example.com/deep2, example.com/huge",
		"⇄ needed by other deps too", "◌ only init() runs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("colour codes written to a non-terminal")
	}
}

func TestWhyFlagsEitherSide(t *testing.T) {
	for _, args := range [][]string{{"-C", app, "why", "tiny"}, {"why", "tiny", "-C", app}} {
		code, out, errs := runHeft(args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errs)
		}
		if !strings.Contains(out, "tiny.PadLeft  ← main.main") {
			t.Errorf("%v: missing entry point:\n%s", args, out)
		}
	}
}

func TestJSON(t *testing.T) {
	code, out, errs := runHeft("-C", app, "-json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var v struct {
		Direct []struct {
			Path    string   `json:"path"`
			Verdict string   `json:"verdict"`
			Drops   []string `json:"drops"`
		} `json:"direct"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if len(v.Direct) != 5 || v.Direct[0].Path != "example.com/big" || v.Direct[0].Verdict != "heavy" {
		t.Fatalf("unexpected: %+v", v.Direct)
	}
}

func TestFailOn(t *testing.T) {
	if code, _, _ := runHeft("-C", app, "-fail-on", "heavy"); code != 1 {
		t.Fatalf("want exit 1 for -fail-on heavy, got %d", code)
	}
	if code, _, _ := runHeft("-C", app, "-fail-on", "no-calls"); code != 0 {
		t.Fatalf("want exit 0 for -fail-on no-calls, got %d", code)
	}
	// A typo must not turn into a CI guard that never fires.
	if code, _, errs := runHeft("-C", app, "-fail-on", "heavey"); code != 2 || !strings.Contains(errs, "unknown verdict") {
		t.Fatalf("want exit 2 for a misspelled verdict, got %d: %s", code, errs)
	}
}

func TestErrors(t *testing.T) {
	if code, _, errs := runHeft("-C", "internal/weigh/testdata/lib"); code != 2 || !strings.Contains(errs, "no main packages") {
		t.Fatalf("library: exit %d, %s", code, errs)
	}
	if code, _, _ := runHeft("-C", app, "why"); code != 2 {
		t.Fatalf("why without module: exit %d", code)
	}
	if code, _, errs := runHeft("-C", app, "why", "nope"); code != 2 || !strings.Contains(errs, "no module matching") {
		t.Fatalf("why nope: exit %d, %s", code, errs)
	}
}
