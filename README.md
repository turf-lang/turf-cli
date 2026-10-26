# turf

`turf` is the command-line tool for [TurfLang](https://github.com/turf-lang/turf-lang). One
binary creates a project, builds it, runs its tests, formats its code and
manages its dependencies, and it also compiles a single file when that is all
you want.

```sh
turf new hello && cd hello
turf run
```

It is written in Go, has one dependency
([BurntSushi/toml](https://github.com/BurntSushi/toml)), and needs the
system `git` only when a project uses git dependencies.

## What it does and does not do

`turf` handles files, the network and reproducibility: project layout,
dependency resolution, the cache, the lock file. Everything that depends on
TurfLang source is the compiler's job. `turf` runs `turfc`, the compiler, for
that. It never parses TurfLang and never rewrites a compiler diagnostic, so the
errors you see are the compiler's own, word for word.

## Commands

### Any file

`turf <file.tr> [flags]` is `turfc <file.tr> [flags]`: same output, same exit
status. Any other argument starting with `-` is forwarded unchanged.

| Command | Does |
|---|---|
| `turf compile <file.tr> [flags]` | the same, spelled out; `--help` lists the flags |
| `turf run <file.tr> [flags] [-- args]` | compile to a temporary executable, run it, delete it; exits with the program's status |
| `turf check <file.tr>` | print every diagnostic a compile would, write no output |
| `turf dump <what> <file.tr>` | show an intermediate form: `ast`, `resolved`, `typechecked`, `modules`, `symbols` or `instantiations` |
| `turf fmt [--write \| --check] [--exclude <path>] [paths...]` | format source; with no path inside a project, its `src/` and `tests/` |
| `turf explain <code>` | explain an error code |
| `turf migrate <name> [--write] <files...>` | migrate source across a language change (`pub`) |
| `turf version`, `turf help [command]` | |

### Projects

A project is a directory tree with a `turf.toml`. These commands work anywhere
inside one; `turf` looks upward for the nearest manifest.

| Command | Does |
|---|---|
| `turf new <name> [--lib]` | create `<name>/` with `turf.toml`, `src/main.tr` (`src/<name>.tr` for a library) and a `.gitignore` |
| `turf init [--lib] [name]` | the same, in the current directory; never overwrites a source file |
| `turf build [--offline] [--locked] [flags]` | resolve dependencies, keep `turf.lock` current, compile; a program lands in `build/<name>`, a library is only checked |
| `turf run [--offline] [--locked] [flags] [-- args]` | with no `.tr` argument: build, then run |
| `turf check [--offline] [--locked] [flags]` | with no `.tr` argument: build's diagnostics, no executable |
| `turf test [paths...] [--offline] [--locked]` | run every test under `tests/`, or only the named files and directories |
| `turf clean` | remove `build/` |
| `turf graph [--project <dir>] [--unsafe] [--offline] [--locked]` | print the package graph as JSON on stdout, for editors and other tools; writes nothing. `--unsafe` adds each package's unsafe surface, counted by the compiler |
| `turf audit [--project <dir>] [--files] [--offline] [--locked]` | a table of where the unsafe code is: per package (and the standard library), its `unsafe module;` files, `unsafe { }` blocks, `extern fn`s and C libraries, and whether `[unsafe] allow` grants it; writes nothing |
| `turf add <name> --path <dir>` | add a path dependency |
| `turf add <name> --git <url> (--tag T \| --branch B \| --rev R)` | fetch and check a git dependency, then add it |
| `turf remove <name>` | remove a dependency |
| `turf fetch [--offline] [--locked]` | download and verify every git dependency, write `turf.lock`, compile nothing |
| `turf update [name...]` | re-resolve the named git dependencies (all with no names); the only command that moves a pinned commit |

`-o` and `--package-graph` are refused in a project build, since `turf` owns
both. `--offline` and `--locked` belong to `turf` too and are never passed on
to the compiler.

Tests are judged by file name and `// EXPECT-...` comments; `turf help test`
has the full rules. A project with no tests is an error, never a silent pass.

There is no package registry. A library is published by pushing a git tag, and
`turf publish` is reserved for later.

## Dependencies

A dependency is `{ path = "..." }` or `{ git = "<url>", tag | rev | branch = "..." }`
(exactly one of the three). The system `git` does the fetching, with prompting
disabled so a private repository fails with git's own message instead of
hanging; any URL git accepts works, including a local path.

**`turf.lock`** sits next to the root `turf.toml`, is written by `turf` and is
meant to be committed. A dependency's own lock file is ignored.

- **Pinned.** A build uses a locked commit as it is, without touching the
  network when the tree is cached. A tag re-pointed upstream or a branch that
  moved is invisible to `build`; only `turf update` moves it.
- **Verified.** Every build re-hashes each cached git tree against the lock. A
  hand-edited file in the cache is an error that names the file and how to
  recover. The hash covers names and contents only, not timestamps or
  permissions.
- **`--offline`** runs no `clone` or `fetch`; anything not both locked and
  cached is an error saying it needs the network.
- **`--locked`** requires the lock computed for this build to equal the file on
  disk, names each difference, and writes nothing. A missing `turf.lock` is an
  error. This is the flag for CI.
- **One version per package.** If two dependents want one git package at
  different commits, the error names both and both refs.

The cache is `$TURF_CACHE_DIR`, else `<user cache dir>/turf`. It is disposable
and an interrupted fetch cannot corrupt it: trees are extracted into a
temporary directory and renamed into place.

## Installing

`turf` is released together with the compiler, so a release archive already
holds both, side by side. Installers and archives are on the
[TurfLang releases page](https://github.com/turf-lang/turf-lang/releases).

To build `turf` by itself you need Go:

```sh
go build -o turf ./cmd/turf
```

## Finding the compiler

First match wins:

1. `$TURF_COMPILER`
2. `turfc` beside the running `turf` (symlinks resolved)
3. `$TURF_HOME/bin/turfc`
4. `turfc` on `PATH`

Before using it, `turf` runs `turfc --version` and refuses a compiler whose
driver interface does not fit: a different major version, or a minor older than
this `turf` requires. A differing release version is fine. `turf` and
`turfc` are versioned independently, and `turf version` prints both.

`turf graph` (without `--unsafe`) and `turf fetch` never run the compiler, so they work without one. `turf audit` and `turf graph --unsafe` need a compiler of driver interface 1.12 or later.

## Development

```sh
go vet ./... && go test ./...
```

The code is laid out by job: `cmd/turf` holds the commands; `internal/manifest`
reads and edits `turf.toml` (unknown keys are errors, and edits are made as
text so your comments survive); `internal/project` builds the package graph;
`internal/gitdep` fetches and extracts; `internal/treehash` and
`internal/lockfile` define the hash and the lock; `internal/driver` runs the
compiler; `internal/version` holds this tool's version and the compiler
interface it needs.

The compiler's repository builds `turf` from a checkout of this one and runs
end-to-end smoke tests against it, so changes that touch the compiler
interface are verified there.
