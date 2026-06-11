// Package modfind is the centralized Go module discovery primitive for tinywasm
// tooling. It runs `go list -m -json all` once per project root, caches the
// parsed result, and classifies each module as writable (main module or local
// replace — tooling may generate files there) or read-only (module cache).
//
// It replaces the byte-identical `go list -m -json all` loops previously
// copy-pasted in ssr, image/min and imagemin. Sibling to depfind: depfind maps
// the package import graph; modfind enumerates modules.
package modfind

// Module is one Go module on disk, classified for tooling.
type Module struct {
	Path      string // import path, e.g. "github.com/veltylabs/item-catalog"
	Dir       string // absolute on-disk dir (cache path or local replace path)
	Version   string // empty for Main and for local replace targets
	IsMain    bool   // the root module of the project at rootDir
	IsReplace bool   // satisfied by a local (filesystem) replace directive
	Indirect  bool   // transitive dependency (not a direct require)
}

// Writable reports whether tooling may generate files inside Dir. True for the
// main module and for local replace targets; false for the read-only module
// cache.
func (m Module) Writable() bool { return m.IsMain || m.IsReplace }
