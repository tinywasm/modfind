# modfind — Architecture

> **What.** `modfind` is the centralized Go module discovery primitive for tinywasm tooling. It runs
> `go list -m -json all` **once** per project root, caches the parsed result, and classifies every
> module as **writable** or **read-only** so a tool can decide whether it may generate files there.
>
> **Why.** Before `modfind`, the byte-identical `go list -m -json all` loop was copy-pasted in three
> places (`ssr`, `image/min`, `imagemin`); each re-ran the costly command at startup and none
> classified writable-vs-read-only. `modfind` is the single source of truth they all consume — one
> `go list` per session, shared. Design rationale (why a dedicated package and not `depfind`,
> `devflow`, or the orchestrator): [DESIGN.md](DESIGN.md).

## The model

```go
type Module struct {
    Path      string // import path, e.g. "github.com/veltylabs/item-catalog"
    Dir       string // absolute on-disk dir (cache path or local replace path)
    Version   string // empty for Main and for local replace targets
    IsMain    bool   // the root module of the project at rootDir
    IsReplace bool   // satisfied by a local (filesystem) replace directive
    Indirect  bool   // transitive dependency
}

func (m Module) Writable() bool // IsMain || IsReplace
```

`Writable()` is the contract a tool relies on:

| `Writable()` | Module kind | What tooling may do |
|---|---|---|
| `true` | main module, or local `replace` target | **generate files in `Dir`** (e.g. ormc writes `model_orm.go`; assetmin writes assets) |
| `false` | read-only module cache | **read `Dir` only** — never write |

## Classification (from `go list -m -json`, not from re-parsing `go.mod`)

Each streamed JSON record maps to a `Module`:

- `Main: true` → `IsMain` (always writable).
- `Replace` with an empty replacement `Version` and a non-empty `Dir` → `IsReplace`; the **replacement
  dir** becomes `Dir` (a filesystem replace is local and writable). A replace to another module
  *version* stays read-only.
- Empty `Dir` (not downloaded / pruned) → the record is **skipped** (nothing on disk to scan).

## The finder

```go
type Finder struct { /* rootDir → []Module cache, mutex, injectable runner */ }

func New() *Finder
func (f *Finder) SetLog(fn func(...any))
func (f *Finder) Discover(rootDir string) ([]Module, error) // go list once, then cached
func (f *Finder) Dirs(rootDir string) ([]string, error)     // convenience: just the dirs
func (f *Finder) Refresh(rootDir string)                    // invalidate after a go.mod change
func (f *Finder) Seed(rootDir string, mods []Module)        // test seam: preload, bypass go list
```

In a tool with several consumers (assets + schema), construct **one** `*Finder` and inject it into
each (`ssr`, `image`, `ormc`) so `go list` runs a single time per session. Each consumer then does its
own per-dir work (extract assets, sync schemas) — `modfind` only enumerates and classifies; it knows
nothing about `model.go`, `model_orm.go`, or assets.

## Constraints

- **Compiles everywhere, runs tool-side only.** No build tags: `os/exec`/`encoding/json` etc. all
  compile under `GOARCH=wasm`, so importers (`ssr`, `image`, `ormc`) that are themselves compile-
  checked under wasm keep building. `Discover` is only ever **called** by tool-side code (never by a
  shipped wasm client), so `go list` never runs in a browser. (An earlier `//go:build !wasm` tag was
  removed: it made the package empty under wasm and broke every untagged importer's wasm build.)
- **Minimal dependencies.** Stdlib + `github.com/tinywasm/fmt` only. `modfind` sits **below** `ssr`,
  `assetmin`, `image`, and `ormc` in the graph and must never import `devflow`, `depfind`, or any
  heavy package.
- **One `go list`, cached.** `Discover` runs the command on first call per `rootDir`; subsequent calls
  hit the cache. `Refresh` invalidates (e.g. on a `go.mod` watcher event).

## Role in the ecosystem

Sibling to [`depfind`](https://github.com/tinywasm/depfind): different axes of the Go build graph.

| | `depfind` | `modfind` |
|---|---|---|
| Granularity | packages + import edges | modules + on-disk dir |
| Question | "which main must recompile?" | "where is each module, and may I write there?" |
| `go list` mode | packages (`./...`) | modules (`-m -json all`) |

Consumers: `ssr` (asset discovery), `image/min` (image discovery), `orm/ormc` (external-module schema
sync), and `app` (constructs the one shared finder and injects it).
