//go:build !wasm

package modfind

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"sync"

	"github.com/tinywasm/fmt"
)

// runner executes `go list -m -json all` in dir and returns raw stdout.
// Injectable so tests can supply canned JSON without a real toolchain.
type runner func(dir string) ([]byte, error)

func goListRunner(dir string) ([]byte, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Finder runs `go list -m -json all` once per rootDir and caches the result.
type Finder struct {
	mu    sync.Mutex
	cache map[string][]Module
	run   runner
	log   func(...any)
}

// New returns a Finder using the real `go list` runner.
func New() *Finder {
	return &Finder{
		cache: make(map[string][]Module),
		run:   goListRunner,
		log:   func(...any) {},
	}
}

// SetLog sets the warning sink. If unset, warnings are discarded.
func (f *Finder) SetLog(fn func(...any)) {
	if fn != nil {
		f.log = fn
	}
}

// Discover returns all modules visible from rootDir (cached after first call).
// Modules with no on-disk Dir are skipped.
func (f *Finder) Discover(rootDir string) ([]Module, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if mods, ok := f.cache[rootDir]; ok {
		return mods, nil
	}

	out, err := f.run(rootDir)
	if err != nil {
		return nil, fmt.Err("modfind: go list -m -json all failed in", rootDir, ":", err)
	}

	mods, err := parse(out)
	if err != nil {
		return nil, fmt.Err("modfind: parsing go list output:", err)
	}

	f.cache[rootDir] = mods
	return mods, nil
}

// Refresh invalidates the cache for rootDir (call after a go.mod change).
func (f *Finder) Refresh(rootDir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cache, rootDir)
}

// Seed pre-populates the cache for rootDir, bypassing `go list`. It is the
// cross-package test seam: a consumer's tests inject a fixed module set without
// a real toolchain round-trip. A subsequent Discover(rootDir) returns mods.
func (f *Finder) Seed(rootDir string, mods []Module) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cache[rootDir] = mods
}

// Dirs returns just the Dir of every discovered module — the []string shape
// ssr/image/imagemin previously produced. Order matches Discover.
func (f *Finder) Dirs(rootDir string) ([]string, error) {
	mods, err := f.Discover(rootDir)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(mods))
	for _, m := range mods {
		dirs = append(dirs, m.Dir)
	}
	return dirs, nil
}

// listEntry mirrors the relevant fields of `go list -m -json` records.
type listEntry struct {
	Path     string
	Version  string
	Dir      string
	Main     bool
	Indirect bool
	Replace  *struct {
		Path    string
		Version string
		Dir     string
	}
}

// parse decodes the streamed (concatenated, not array) JSON objects emitted by
// `go list -m -json all` into classified Modules, skipping records with no Dir.
func parse(out []byte) ([]Module, error) {
	var mods []Module
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var e listEntry
		if err := dec.Decode(&e); err != nil {
			return nil, err
		}

		m := Module{
			Path:     e.Path,
			Dir:      e.Dir,
			Version:  e.Version,
			IsMain:   e.Main,
			Indirect: e.Indirect,
		}

		// A local (filesystem) replace carries a replacement with no Version.
		// Its Dir is the writable local path; prefer it over the original.
		if e.Replace != nil && e.Replace.Version == "" && e.Replace.Dir != "" {
			m.IsReplace = true
			m.Dir = e.Replace.Dir
		}

		if m.Dir == "" {
			continue // not on disk (pruned / not downloaded) — cannot scan
		}
		mods = append(mods, m)
	}
	return mods, nil
}
