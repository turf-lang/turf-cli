package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/turf-lang/turf-cli/internal/manifest"
)

const graphUsage = "usage: turf graph [--project <dir>] [--unsafe] [--offline] [--locked]"

// cmdGraph prints the project's package graph (the JSON format of
// src/tooling/PackageGraph.h) on stdout and writes nothing.
//
// It exists for tools that analyse a project without building it: the language
// server hands this graph to the compiler so that `import` resolves in the
// editor exactly as it does in `turf build` -- the package's own modules,
// its dependencies, the standard library. Unlike `turf build` it needs no
// compiler (the graph is a function of the manifests alone), touches neither
// turf.lock nor build/, and includes the root package's tests/ directory when
// there is one, since an editor opens test files too.
//
// Dependencies are resolved as for a build, so a git dependency that is not in
// the fetch cache is fetched unless --offline says not to. A tool that must
// never reach the network passes --offline.
//
// --unsafe adds each package's unsafe surface (LB-2), which only the compiler
// can count, so that form does need a compiler. Its output is a report, not
// something to hand back to turfc: see Graph.JSONWithUnsafe.
func cmdGraph(args []string) int {
	nf, rest := splitNetFlags(args)
	start := ""
	unsafe := false
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--unsafe":
			unsafe = true
		case a == "--project":
			if i+1 >= len(rest) {
				return usagef("graph: --project needs a directory (%s)", graphUsage)
			}
			i++
			start = rest[i]
		case strings.HasPrefix(a, "--project="):
			start = strings.TrimPrefix(a, "--project=")
		default:
			return usagef("graph: unexpected argument '%s' (%s)", a, graphUsage)
		}
	}
	var m *manifest.Manifest
	var code int
	if start == "" {
		m, code = loadRootManifest()
	} else {
		abs, err := filepath.Abs(start)
		if err != nil {
			return failf("graph: %v", err)
		}
		m, code = loadManifestFrom(abs)
	}
	if code != 0 {
		return code
	}
	g, _, err := resolveGraph(m, nf, nil)
	if err != nil {
		return failf("%v", err)
	}

	withTests := false
	if st, err := os.Stat(filepath.Join(g.Root.Dir, testsDirName)); err == nil && st.IsDir() {
		withTests = true
	}
	render := g.JSON
	if withTests {
		render = g.JSONWithTests
	}
	if unsafe {
		code := withCompiler(func(compiler string) int {
			r, err := collectUnsafeReport(g, compiler, "turf graph --unsafe")
			if err != nil {
				return failf("%v", err)
			}
			render = func() ([]byte, error) { return g.JSONWithUnsafe(withTests, r.Raw) }
			return 0
		})
		if code != 0 {
			return code
		}
	}
	data, err := render()
	if err != nil {
		return failf("%v", err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return failf("cannot write the graph: %v", err)
	}
	return 0
}

const helpGraph = `turf graph - print the project's package graph

Usage:
  turf graph [--project <dir>] [--unsafe] [--offline] [--locked]

Run inside a project, or give --project <dir> (a directory in it: the project is
found upward from there, as everywhere else).

Prints, as JSON on stdout, the graph 'turf build' hands the compiler: the root
package, its dependencies (path and git, resolved as for a build) and where
each package's src/ is, with the root's tests/ if the project has one. Writes
nothing (no turf.lock, no build/), and needs no compiler, so editors and other
tools can ask for it. The format is documented in src/tooling/PackageGraph.h.

--unsafe adds where each package's unsafe code is: an "unsafe" object in every
package and a top-level "stdlib" with the standard library's, counted by the
compiler (turfc --unsafe-report, driver interface 1.12), so this form needs
one. 'turf audit' prints the same as a table. The result is a report: turfc
does not accept it back as --package-graph.

--offline runs no network git command (a git dependency missing from the fetch
cache is then an error); --locked requires turf.lock to be present. Both apply
as they do to 'turf build' (see 'turf help build').
`
