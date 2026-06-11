# modfind
<img src="docs/img/badges.svg">

Centralized Go module discovery + replace/cache classification for tinywasm tooling.

Runs `go list -m -json all` **once** per project root, caches it, and classifies each module as
**writable** (main module or local `replace` → tooling may generate files there) or **read-only**
(module cache → read only). Replaces the byte-identical `go list -m -json all` loops previously
copy-pasted in `ssr`, `image/min`, and `imagemin`.

Sibling to [`depfind`](https://github.com/tinywasm/depfind): `depfind` maps the **package import
graph** ("which main to recompile"); `modfind` enumerates **modules** ("where each module lives, and
may I write there"). Called only by tool-side code (compiles everywhere, no build tags); deps:
stdlib + `tinywasm/fmt`.

## Usage

```go
f := modfind.New()
mods, err := f.Discover(rootDir) // []modfind.Module, cached after first call

for _, m := range mods {
    if m.Writable() {
        // main module or local replace → generate files in m.Dir
    } else {
        // read-only cache → only read m.Dir
    }
}

dirs, _ := f.Dirs(rootDir) // []string convenience (drop-in for old []dir loops)
f.Refresh(rootDir)         // invalidate after a go.mod change
```

In a tool with several consumers (assets + schema), construct **one** `*modfind.Finder` and inject it
into each (ssr, image, ormc) so `go list` runs a single time per session.

## Module

| Field | Meaning |
|---|---|
| `Path` | import path |
| `Dir` | on-disk dir (cache path or local replace path) |
| `Version` | empty for main / local replace |
| `IsMain` | the project's root module |
| `IsReplace` | satisfied by a local filesystem `replace` (writable) |
| `Indirect` | transitive dependency |
| `Writable()` | `IsMain || IsReplace` |

## Docs

- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — what modfind is: the `Module` model, the
  writable/read-only classification, the finder API, and constraints.
- [docs/DESIGN.md](docs/DESIGN.md) — why a dedicated package (not `depfind`, `devflow`, or `app`) and
  the duplication it removes.
