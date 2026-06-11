package modfind

import "testing"

// canned `go list -m -json all` output: a main module, a cache dep, a pruned
// dep (no Dir), and a local replace.
const cannedJSON = `{
	"Path": "github.com/tinywasm/example",
	"Main": true,
	"Dir": "/home/u/dev/example",
	"GoVersion": "1.25.2"
}
{
	"Path": "github.com/some/dep",
	"Version": "v1.2.0",
	"Indirect": true,
	"Dir": "/home/u/go/pkg/mod/github.com/some/dep@v1.2.0"
}
{
	"Path": "github.com/pruned/dep",
	"Version": "v0.1.0",
	"Indirect": true
}
{
	"Path": "github.com/veltylabs/item-catalog",
	"Version": "v0.0.2",
	"Dir": "/home/u/go/pkg/mod/github.com/veltylabs/item-catalog@v0.0.2",
	"Replace": {
		"Path": "../modules/item-catalog",
		"Dir": "/home/u/dev/modules/item-catalog"
	}
}`

func fakeFinder() *Finder {
	f := New()
	f.run = func(string) ([]byte, error) { return []byte(cannedJSON), nil }
	return f
}

func TestDiscoverClassification(t *testing.T) {
	f := fakeFinder()
	mods, err := f.Discover("/root")
	if err != nil {
		t.Fatal(err)
	}
	// pruned dep (no Dir) must be skipped → 3 of 4 records.
	if len(mods) != 3 {
		t.Fatalf("want 3 modules, got %d: %+v", len(mods), mods)
	}

	by := map[string]Module{}
	for _, m := range mods {
		by[m.Path] = m
	}

	main := by["github.com/tinywasm/example"]
	if !main.IsMain || !main.Writable() {
		t.Errorf("main module not classified writable: %+v", main)
	}

	cache := by["github.com/some/dep"]
	if cache.IsMain || cache.IsReplace || cache.Writable() {
		t.Errorf("cache dep should be read-only: %+v", cache)
	}

	repl := by["github.com/veltylabs/item-catalog"]
	if !repl.IsReplace || !repl.Writable() {
		t.Errorf("local replace not classified writable: %+v", repl)
	}
	if repl.Dir != "/home/u/dev/modules/item-catalog" {
		t.Errorf("replace must use the replacement Dir, got %q", repl.Dir)
	}
}

func TestDiscoverCachesAndRefresh(t *testing.T) {
	f := New()
	calls := 0
	f.run = func(string) ([]byte, error) { calls++; return []byte(cannedJSON), nil }

	_, _ = f.Discover("/root")
	_, _ = f.Discover("/root")
	if calls != 1 {
		t.Fatalf("expected 1 go list call (cached), got %d", calls)
	}

	f.Refresh("/root")
	_, _ = f.Discover("/root")
	if calls != 2 {
		t.Fatalf("expected re-run after Refresh, got %d calls", calls)
	}
}

func TestDirs(t *testing.T) {
	f := fakeFinder()
	dirs, err := f.Dirs("/root")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 3 {
		t.Fatalf("want 3 dirs, got %d", len(dirs))
	}
}

func TestSeedBypassesGoList(t *testing.T) {
	f := New()
	f.run = func(string) ([]byte, error) { t.Fatal("go list must not run after Seed"); return nil, nil }
	f.Seed("/root", []Module{{Path: "x", Dir: "/x", IsMain: true}})
	mods, err := f.Discover("/root")
	if err != nil || len(mods) != 1 || !mods[0].Writable() {
		t.Fatalf("seed not honored: %+v err=%v", mods, err)
	}
}
