package weigh

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// The fixture in testdata/ is a small multi-module world wired together with
// replace directives, so these tests need no network:
//
//	app ──► tiny                        (uses 1 of 3 small funcs)
//	    ──► big ──► big/internal/engine ──► huge ──► deep1, deep2
//	    ──► shared ◄── big              (also needed by big)
//	    ──► _ sidefx                    (blank import, init only)
var (
	fixtureOnce sync.Once
	fixture     *Report
	fixtureErr  error
)

func load(t *testing.T) *Report {
	t.Helper()
	fixtureOnce.Do(func() { fixture, fixtureErr = Analyze(Options{Dir: "testdata/app"}) })
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixture
}

func dep(t *testing.T, r *Report, path string) *Dep {
	t.Helper()
	for _, d := range r.Direct {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("no direct dependency %s", path)
	return nil
}

func TestProgramsAndModules(t *testing.T) {
	r := load(t)
	if r.MainModule != "example.com/app" || !reflect.DeepEqual(r.Programs, []string{"example.com/app"}) {
		t.Fatalf("main module %q, programs %v", r.MainModule, r.Programs)
	}
	var paths []string
	for _, m := range r.Modules {
		paths = append(paths, m.Path)
	}
	want := []string{
		"example.com/big", "example.com/deep1", "example.com/deep2", "example.com/huge",
		"example.com/mega-tiny", "example.com/shared", "example.com/sidefx", "example.com/tiny",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("modules = %v, want %v", paths, want)
	}
	if len(r.Direct) != 5 {
		t.Fatalf("want 5 direct deps, got %d", len(r.Direct))
	}
}

func TestInlineCandidate(t *testing.T) {
	d := dep(t, load(t), "example.com/tiny")
	if d.Funcs != 3 || d.ReachedFuncs != 1 {
		t.Fatalf("tiny: reached %d of %d funcs, want 1 of 3", d.ReachedFuncs, d.Funcs)
	}
	if d.ReachedLines != 6 {
		t.Fatalf("tiny: reached lines %d, want 6 (PadLeft)", d.ReachedLines)
	}
	if d.Verdict != VerdictInline {
		t.Fatalf("tiny verdict %q (%s)", d.Verdict, d.Note)
	}
	if !reflect.DeepEqual(d.Drops, []string{"example.com/tiny"}) {
		t.Fatalf("tiny drops %v", d.Drops)
	}
}

func TestHeavyDependencyAndTransitiveDrops(t *testing.T) {
	d := dep(t, load(t), "example.com/big")
	if d.Funcs != 8 || d.ReachedFuncs != 2 {
		t.Fatalf("big: reached %d of %d funcs, want Greet+Render of 8", d.ReachedFuncs, d.Funcs)
	}
	want := []string{"example.com/big", "example.com/deep1", "example.com/deep2", "example.com/huge"}
	if !reflect.DeepEqual(d.Drops, want) {
		t.Fatalf("big drops %v, want %v (shared must stay: the app imports it too)", d.Drops, want)
	}
	if d.DropReachedLines != 5 { // Greet 3 + Render 1 + huge.Decorate 1
		t.Fatalf("big drop reached lines = %d, want 5", d.DropReachedLines)
	}
	if d.Verdict != VerdictHeavy {
		t.Fatalf("big verdict %q (%s)", d.Verdict, d.Note)
	}
}

func TestSharedDependencyFreesNothing(t *testing.T) {
	d := dep(t, load(t), "example.com/shared")
	if len(d.Drops) != 0 || d.DropLines != 0 {
		t.Fatalf("shared drops %v (%d lines); big still needs it", d.Drops, d.DropLines)
	}
	if d.Verdict != VerdictShared {
		t.Fatalf("shared verdict %q", d.Verdict)
	}
}

func TestInitOnly(t *testing.T) {
	d := dep(t, load(t), "example.com/sidefx")
	if d.ReachedFuncs != 0 || d.InitFuncs != 1 || d.Verdict != VerdictInitOnly {
		t.Fatalf("sidefx: reached %d, inits %d, verdict %q", d.ReachedFuncs, d.InitFuncs, d.Verdict)
	}
}

func TestIndirectModulesAreAttributed(t *testing.T) {
	r := load(t)
	huge, err := r.Lookup("huge")
	if err != nil {
		t.Fatal(err)
	}
	if huge.Direct || huge.ReachedFuncs != 1 || huge.Funcs != 5 {
		t.Fatalf("huge: direct=%v reached %d of %d, want indirect 1 of 5", huge.Direct, huge.ReachedFuncs, huge.Funcs)
	}
	if r.ThirdReached != 13 { // tiny 6 + big 4 + huge 1 + shared 1 + mega-tiny 1
		t.Fatalf("third-party reached lines = %d, want 12", r.ThirdReached)
	}
}

func TestEntries(t *testing.T) {
	r := load(t)
	big, _ := r.Lookup("example.com/big")
	got := r.Entries(big)
	if len(got) != 1 || got[0].Callee != "big.Greet" || got[0].Caller != "main.main" || got[0].Line != 21 {
		t.Fatalf("entries into big = %+v", got)
	}
	shared, _ := r.Lookup("shared")
	got = r.Entries(shared)
	if len(got) != 1 || got[0].Callee != "shared.Upper" || got[0].Caller != "big.Greet" {
		t.Fatalf("entries into shared = %+v (called by big, not the app)", got)
	}
}

func TestLookup(t *testing.T) {
	r := load(t)
	// "tiny" is a path suffix of example.com/tiny and a substring of
	// example.com/mega-tiny; the suffix match must win.
	if m, err := r.Lookup("tiny"); err != nil || m.Path != "example.com/tiny" {
		t.Fatalf("Lookup(tiny) = %v, %v", m, err)
	}
	if m, err := r.Lookup("mega"); err != nil || m.Path != "example.com/mega-tiny" {
		t.Fatalf("Lookup(mega) = %v, %v", m, err)
	}
	if _, err := r.Lookup("deep"); err == nil {
		t.Fatal("ambiguous lookup should fail")
	}
	if _, err := r.Lookup("nope"); err == nil {
		t.Fatal("missing lookup should fail")
	}
}

func TestLibraryIsRejected(t *testing.T) {
	_, err := Analyze(Options{Dir: "testdata/lib"})
	if !errors.Is(err, ErrNoMain) {
		t.Fatalf("want ErrNoMain, got %v", err)
	}
}

func TestHuman(t *testing.T) {
	for n, want := range map[int]string{0: "0", 950: "950", 1234: "1.2k", 48211: "48k", 2_300_000: "2.3M"} {
		if got := Human(n); got != want {
			t.Errorf("Human(%d) = %q, want %q", n, got, want)
		}
	}
}
