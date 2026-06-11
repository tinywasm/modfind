# modfind — Design rationale

Why module discovery is its **own** package, rather than folded into an existing one. See
[ARCHITECTURE.md](ARCHITECTURE.md) for what `modfind` is.

## The duplication it removes

The same `go list -m -json all` → decode → collect-dirs loop was copy-pasted, byte-identical, in:

- `ssr/extract.go` (`discoverModules`)
- `image/min/loader.go` (`listModulesReal`)
- `imagemin/loader.go` (`listModulesReal`)

Plus `app` carried `ListModulesFn` plumbing to inject it. Three independent `go list` runs at startup
for the same project, no shared cache, and **no** writable-vs-read-only classification — so no
consumer could answer "may I generate a file in this module's dir?".

## Why not fold it into an existing package

**Not `depfind`.** `depfind` answers a different axis — package-level reverse dependencies ("which
main must recompile when a file changes"). It is lightweight (zero external deps) and already shells
out to `go list`, so coupling was not the blocker. The blocker is **cohesion + consumer semantics**:
module enumeration is a different granularity (module dir + replace/cache class) from import-graph
analysis. `ssr`/`image`/`ormc` want "list module dirs"; they do **not** want `depfind`'s reverse-dep
engine (`ThisFileIsMine`, `FindReverseDeps`) in their API surface. An `ssr → depfind` edge would
falsely read "ssr depends on reverse-dependency analysis." `ssr → modfind` reads correctly.

**Not `devflow`.** `devflow` already has go.mod handling (`GetReplacePaths`) but is the heavy dev
grab-bag (keyring, dbus, wincred, gorun, wizard). `ssr`/`image`/`orm` do **not** import it today;
forcing them to, just to list modules, would drag OS keyring + dbus into mid-level libraries. That is
the coupling trap a dedicated package avoids.

**Not `app`.** `app` is the orchestrator. Putting discovery there keeps it non-reusable (each of
ssr/image/ormc would still need its own), and makes `app` more than a wirer.

## Why a dedicated lightweight package wins

- One `go list` per session, shared by all consumers → the light/fast dev loop the tooling targets.
- Stdlib + `tinywasm/fmt` only, so any mid-level library imports it cheaply, with honest dependency
  semantics.
- `app` stays a pure orchestrator (constructs one finder, injects it).
- Reusable across the ecosystem; `devflow` could later consume it to dedupe its own `go list` usage.
- Single, focused responsibility — `depfind` keeps its crisp mission (package graph), `modfind` owns
  module enumeration.

The only cost is one more repository; with the `gonew` scaffolding that is cheap relative to the
maintenance saved by deleting three duplicated copies.
