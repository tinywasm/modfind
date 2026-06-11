# PLAN — `modfind`: centralized Go module discovery + replace/cache classification

> **STATUS: IMPLEMENTED.** `modfind.go`, `discover.go`, `discover_test.go` are written and tests pass
> (incl. a real-project smoke). This document is the design record; the consumer migrations (ssr,
> image/min, imagemin) are dispatched from **their own** repos' plans, not from here.
>
> **Mission.** Run `go list -m -json all` **once**, parse it into a typed `[]Module`, and classify
> each module as **writable** (main module or local `replace` → tooling may generate files there) or
> **read-only** (module cache → tooling may only read). Today this exact `go list -m -json all` block
> is **copy-pasted** in `ssr/extract.go` (`discoverModules`), `image/min/loader.go`
> (`listModulesReal`), and `imagemin/loader.go` (`listModulesReal`); `app` adds `ListModulesFn`
> plumbing; `ormc` lacks it entirely. `modfind` is the single source of truth they all consume.
>
> **Why a dedicated package (not depfind, not devflow, not app).** `depfind` answers a *different
> axis* — package-level reverse dependencies ("which main to recompile"); its consumers don't want a
> module-list API named "reverse dependency finder". `devflow` is too heavy (keyring/dbus/gorun) to be
> a dependency of `ssr`/`imagemin`/`orm`. `app` must stay a pure orchestrator. `modfind` is tiny
> (stdlib + `tinywasm/fmt` only), so any mid-level lib can import it cheaply. Sibling to `depfind`:
> `depfind` = dependency graph, `modfind` = module enumeration.
>
> **Goal: a light, agile, fast dev environment.** `go list -m -json all` is the costly call; running
> it once and sharing the result across ssr/imagemin/ormc/app is the whole point.

Related: consumers each gain a `docs/PLAN.md` migration — `ssr`, `image/min` + `imagemin`,
`orm` (ormc), `app`. Orchestration: `tinywasm/docs/ORM_MASTER_PLAN.md`.

---

## 1. Development Rules (constraints copied for execution context)

- **Minimal deps.** Only the Go stdlib (`os/exec`, `encoding/json`, `bytes`, `path/filepath`,
  `strings`) and `github.com/tinywasm/fmt` for errors/strings. **Never** import `devflow`, `depfind`,
  `ssr`, `assetmin`, or any heavy package — `modfind` sits *below* all of them in the graph.
- **Tool-side only — `//go:build !wasm`.** `os/exec` + `go list` never run in WASM. Every `.go` file
  carries `//go:build !wasm` so a WASM consumer that transitively pulls the module never breaks the
  build. (This is the same constraint `imagemin/loader.go` already uses.)
- **Run `go list` once; cache the result.** A `Finder` caches the parsed `[]Module` per `rootDir`.
  Repeated `Discover(rootDir)` calls return the cache. Expose `Refresh()` to invalidate (e.g. after a
  `go.mod` edit).
- **Classification comes from `go list -m -json`, not from re-parsing `go.mod`.** The JSON already
  carries `Main`, `Replace`, and `Dir`. Do not shell out to `go mod edit` or parse `go.mod` text.
- **Skip modules with empty `Dir`.** Not-downloaded / pruned modules have no `Dir`; they are not on
  disk and cannot be scanned.
- **Zero reflection. `gotest` (not `go test`). Documentation first.**

---

## 2. Problem

Three byte-identical copies of the same loop exist:

```go
// ssr/extract.go · image/min/loader.go · imagemin/loader.go  (the same code, 3x)
cmd := exec.Command("go", "list", "-m", "-json", "all")
cmd.Dir = rootDir
// … decode JSON stream … collect Dir …
```

Each consumer re-runs `go list -m -json all` independently (slow on large dep trees), and **none**
classifies writable-vs-readonly — so no consumer can decide "may I generate a file in this module's
dir?". `ormc` needs exactly that distinction: a **replace** module (writable) gets `model_orm.go`
generated in place; a **cache** module (read-only) is only read. There is nowhere central to put it.

---

## 3. Decision — the `modfind` API

```go
//go:build !wasm

package modfind

// Module is one Go module on disk, classified for tooling.
type Module struct {
    Path      string // import path, e.g. "github.com/veltylabs/item-catalog"
    Dir       string // absolute on-disk dir (cache path or local replace path)
    Version   string // empty for Main and for replace targets
    IsMain    bool   // the root module of rootDir's project
    IsReplace bool   // satisfied by a local `replace` directive (writable)
    Indirect  bool   // transitive dependency (not a direct require)
}

// Writable reports whether tooling may generate files inside Dir.
// True for the main module and for local replace targets; false for the
// read-only module cache.
func (m Module) Writable() bool { return m.IsMain || m.IsReplace }

// Finder runs `go list -m -json all` once per rootDir and caches the result.
type Finder struct { /* rootDir → []Module cache, mutex, log */ }

func New() *Finder
func (f *Finder) SetLog(fn func(...any))

// Discover returns all modules visible from rootDir (cached after first call).
// Modules with no on-disk Dir are skipped.
func (f *Finder) Discover(rootDir string) ([]Module, error)

// Refresh invalidates the cache for rootDir (call after a go.mod change).
func (f *Finder) Refresh(rootDir string)

// Dirs is a convenience returning just the Dir of every discovered module —
// the shape ssr/imagemin already consume (drop-in for their []string loops).
func (f *Finder) Dirs(rootDir string) ([]string, error)
```

### 3.1 Classification rules (from the `go list -m -json` stream)

Each JSON object maps to a `Module`:

| JSON field | → `Module` | Notes |
|---|---|---|
| `Path` | `Path` | always present |
| `Dir` | `Dir` | **skip the record if empty** (not on disk) |
| `Version` | `Version` | empty for `Main`/replace |
| `Main: true` | `IsMain = true` | the project root module |
| `Replace != nil` | `IsReplace = true`, `Dir = Replace.Dir` | local replace → **writable**; use the *replacement's* Dir |
| `Indirect: true` | `Indirect = true` | transitive |

Decode the streamed JSON objects (the output is a concatenation of objects, not an array — use a
`json.Decoder` loop with `dec.More()`, exactly as the existing copies do).

### 3.2 Why `Writable()` matters (the ormc contract)

`ormc`'s centralized scan (its own plan) asks each module:
- `Writable() == true` (main or replace) → parse `model.go`, **generate `model_orm.go` in place**,
  then sync the schema. Agile local-dev loop, same as assetmin regenerates assets for replace dirs.
- `Writable() == false` (cache) → **only read the committed `model_orm.go`**, extract its schema, and
  sync. Never write to the read-only cache.

`modfind` does not know about `model.go` or assets — it only classifies. Each consumer decides what to
scan. This keeps `modfind` a pure, reusable primitive.

---

## 4. Implementation Steps

### Step 1 — Replace the stub
Replace the `gonew` stub in [modfind.go](../modfind.go) (`type Modfind struct{}`) with the `Finder`
type, `Module`, and the `New`/`SetLog`/`Discover`/`Refresh`/`Dirs` methods (§3). Add the
`//go:build !wasm` tag to every file.

### Step 2 — `go list` runner + JSON decode
New [discover.go](../discover.go): exec `go list -m -json all` (with `cmd.Dir = rootDir`) and decode
its **streamed** output (concatenated JSON objects, not an array → `json.Decoder` + `dec.More()`
loop). Map each record into `[]Module` with the §3.1 classification. Cache by `rootDir`; guard with a
mutex. The runner is an injectable field (`func(dir string) ([]byte, error)`) so tests supply canned
JSON.

### Step 3 — `Writable()` + `Dirs()` helpers
`Module.Writable()` and `Finder.Dirs()` (§3). `Dirs()` exists so `ssr`/`imagemin` migrate with a
one-line change (they currently want `[]string`).

### Step 4 — Documentation
[README.md](../README.md): purpose, the dedup story (3 copies → 1), the `Writable()` contract, and a
short usage snippet. [docs/ARCHITECTURE.md](ARCHITECTURE.md): the "why a dedicated package" rationale
(condensed from this plan's header). Link both from README.

---

## 5. Edge Cases

- **Module with empty `Dir`** (not downloaded) → skipped (§3.1). Not an error.
- **`go list` fails** (broken `go.mod`, offline) → return the error; consumers log-and-continue (they
  already do: "warning: failed to list modules").
- **`Replace` to a local path** → `IsReplace = true`, `Dir` = the replacement dir (writable). A
  `replace` to *another module version* (not a local path) has a non-empty `Replace.Version` and a
  cache `Dir` → treat as read-only (`IsReplace` only when the replacement Dir is a local path outside
  the cache). Document this distinction.
- **`Main` module** → `IsMain = true`, always `Writable()`.
- **Repeated `Discover` calls** → served from cache; only the first shells out.
- **Concurrent consumers** (ssr + imagemin + ormc at startup) → the mutex serializes the first
  `go list`; the rest hit the cache.

---

## 6. Test Strategy

`gotest` in `modfind/tests/` using a fixture project (a tiny throwaway module dir with a `replace`).

| # | Case | Assert |
|---|------|--------|
| M1 | `Discover` on a real module dir | returns `>0` modules; `IsMain` set on exactly one |
| M2 | cache module (under GOMODCACHE) | `Writable() == false`, `Dir` non-empty |
| M3 | local `replace` target | `IsReplace == true`, `Writable() == true`, `Dir` = replacement path |
| M4 | module with empty `Dir` in JSON | skipped (not in result) |
| M5 | second `Discover` (same rootDir) | no second `go list` (record exec count via injected runner) |
| M6 | `Refresh` then `Discover` | re-runs `go list` |
| M7 | `Dirs()` | returns the `Dir` of each `Discover` module, same order |
| M8 | `go list` error (bad rootDir) | error returned, no panic |

> Inject the `go list` runner (a `func(dir string) ([]byte, error)` field defaulting to the real
> exec) so M5/M6/M8 can use canned JSON without a real toolchain round-trip.

---

## 7. Out of Scope

- Scanning module dirs for `model.go` / `model_orm.go` / assets — that belongs to each **consumer**
  (`ormc`, `ssr`, `imagemin`), which call `Discover` and then do their own per-dir work.
- Reverse-dependency / file-ownership analysis — that is `depfind`'s job (different axis).
- Watching `go.mod` for changes — the consumer calls `Refresh()` when its watcher reports a `go.mod`
  event.
- Any WASM-side behavior — `modfind` is `//go:build !wasm` tool-only.
