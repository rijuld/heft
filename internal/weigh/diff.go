package weigh

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Delta is what changed in a program's dependencies between two reports.
type Delta struct {
	SchemaVersion int    `json:"schema_version"`
	Base          string `json:"base"`
	// Third-party lines compiled in, before and after.
	BaseLines int `json:"base_lines"`
	HeadLines int `json:"head_lines"`
	// Modules that entered or left the build (direct or not).
	Added   []*Module `json:"added_modules"`
	Removed []*Module `json:"removed_modules"`
	// Direct dependencies that are new, or whose verdict changed.
	NewDirect     []*Dep          `json:"new_direct"`
	Changed       []VerdictChange `json:"changed_verdicts"`
	RemovedDirect []string        `json:"removed_direct"`
}

// VerdictChange is a direct dependency whose verdict moved.
type VerdictChange struct {
	*Dep
	Was string `json:"was"`
}

// Diff compares head with base.
func Diff(base, head *Report, baseRef string) *Delta {
	d := &Delta{SchemaVersion: SchemaVersion, Base: baseRef, BaseLines: base.ThirdParty, HeadLines: head.ThirdParty,
		Added: []*Module{}, Removed: []*Module{}, NewDirect: []*Dep{}, Changed: []VerdictChange{}, RemovedDirect: []string{}}
	for _, m := range head.Modules {
		if base.byPath[m.Path] == nil {
			d.Added = append(d.Added, m)
		}
	}
	for _, m := range base.Modules {
		if head.byPath[m.Path] == nil {
			d.Removed = append(d.Removed, m)
		}
	}
	was := map[string]string{}
	for _, dep := range base.Direct {
		was[dep.Path] = dep.Verdict
	}
	isDirect := map[string]bool{}
	for _, dep := range head.Direct {
		isDirect[dep.Path] = true
		v, ok := was[dep.Path]
		switch {
		case !ok:
			d.NewDirect = append(d.NewDirect, dep)
		case v != dep.Verdict:
			d.Changed = append(d.Changed, VerdictChange{dep, v})
		}
	}
	for _, dep := range base.Direct {
		if !isDirect[dep.Path] {
			d.RemovedDirect = append(d.RemovedDirect, dep.Path)
		}
	}
	return d
}

// Flagged lists the direct dependencies a CI gate should look at: the new
// ones and those whose verdict changed.
func (d *Delta) Flagged() []*Dep {
	out := append([]*Dep(nil), d.NewDirect...)
	for _, c := range d.Changed {
		out = append(out, c.Dep)
	}
	return out
}

// AnalyzeAt weighs the program as it was at git revision ref. The tree is
// exported with `git archive` into a temporary directory, so your working
// tree and .git are left alone.
func AnalyzeAt(ref string, opt Options) (*Report, error) {
	dir := opt.Dir
	if dir == "" {
		dir = "."
	}
	git := func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	prefix, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	if _, err := git("rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return nil, fmt.Errorf("-base %q: not a commit in this repository", ref)
	}

	tmp, err := os.MkdirTemp("", "heft-base-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	cmd := exec.Command("git", "archive", "--format=tar", ref)
	cmd.Dir = strings.TrimSpace(string(top))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := untar(stdout, tmp); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("git archive %s: %v", ref, err)
	}

	bopt := opt
	bopt.Dir = filepath.Join(tmp, filepath.FromSlash(strings.TrimSpace(string(prefix))))
	rep, err := Analyze(bopt)
	if err != nil {
		return nil, fmt.Errorf("at %s: %w", ref, err)
	}
	return rep, nil
}

// untar extracts regular files and directories from r into dir, refusing
// anything that would land outside it.
func untar(r io.Reader, dir string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(name) {
			continue // pax headers, or paths escaping dir
		}
		target := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return err
			}
		}
	}
}
