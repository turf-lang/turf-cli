package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/turf-lang/turf-cli/internal/driver"
	"github.com/turf-lang/turf-cli/internal/project"
	"github.com/turf-lang/turf-cli/internal/version"
)

const auditUsage = "usage: turf audit [--project <dir>] [--files] [--offline] [--locked]"

// unsafeReportFormat is the "format" of `turfc --unsafe-report` this turf reads
// (docs/reference/schemas/unsafe-report.schema.json in turf-lang).
const unsafeReportFormat = 1

// unsafeFile and unsafePackage are the report's objects, as turfc writes them.
type unsafeFile struct {
	Path          string   `json:"path"`
	UnsafeModule  bool     `json:"unsafe_module"`
	UnsafeBlocks  int      `json:"unsafe_blocks"`
	ExternFns     int      `json:"extern_fns"`
	LinkLibraries []string `json:"link_libraries"`
	Error         string   `json:"error"`
}

type unsafePackage struct {
	AllowUnsafe   bool         `json:"allow_unsafe"`
	Modules       int          `json:"modules"`
	UnsafeModules int          `json:"unsafe_modules"`
	UnsafeBlocks  int          `json:"unsafe_blocks"`
	ExternFns     int          `json:"extern_fns"`
	LinkLibraries []string     `json:"link_libraries"`
	Files         []unsafeFile `json:"files"`
}

// unsafeReport is one `turfc --unsafe-report` run: the objects read for `turf
// audit`, and the same objects verbatim for `turf graph --unsafe`, which passes
// them through without depending on what is in them.
type unsafeReport struct {
	Packages map[string]unsafePackage
	Stdlib   unsafePackage
	Raw      project.UnsafeSurface
}

// unsafeInterfaceError is nil when the compiler has --unsafe-report. An older
// turfc would take the flag for an unknown option and stop with a usage error
// that says nothing about why turf asked.
func unsafeInterfaceError(info driver.CompilerInfo, compiler, command string) error {
	need := version.RequiredForUnsafeReport
	if info.Interface.Major == need.Major && info.Interface.Minor >= need.Minor {
		return nil
	}
	return fmt.Errorf("`%s` needs a compiler that speaks driver interface %d.%d or a later %d.x "+
		"(it counts the unsafe code), but turfc %s (%s) speaks %d.%d\n"+
		"hint: upgrade turfc, or point TURF_COMPILER at a newer one",
		command, need.Major, need.Minor, need.Major, info.Release, compiler,
		info.Interface.Major, info.Interface.Minor)
}

// collectUnsafeReport has the compiler count g's unsafe surface: the graph goes
// to a temporary file (nothing in the project is written) and turfc's JSON
// comes back on stdout.
func collectUnsafeReport(g *project.Graph, compiler, command string) (*unsafeReport, error) {
	info, err := driver.Probe(compiler)
	if err != nil {
		return nil, err
	}
	if err := unsafeInterfaceError(info, compiler, command); err != nil {
		return nil, err
	}
	data, err := g.JSON()
	if err != nil {
		return nil, fmt.Errorf("cannot render the package graph: %v", err)
	}
	f, err := os.CreateTemp("", "turf-unsafe-graph-*.json")
	if err != nil {
		return nil, fmt.Errorf("cannot write the package graph: %v", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, fmt.Errorf("cannot write the package graph: %v", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("cannot write the package graph: %v", err)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(compiler, "--unsafe-report", "--package-graph", f.Name())
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s --unsafe-report failed: %s", compiler, msg)
	}
	return parseUnsafeReport(stdout.Bytes(), compiler)
}

func parseUnsafeReport(data []byte, compiler string) (*unsafeReport, error) {
	var raw struct {
		Format   int                        `json:"format"`
		Packages map[string]json.RawMessage `json:"packages"`
		Stdlib   json.RawMessage            `json:"stdlib"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s --unsafe-report printed something that is not its JSON report: %v", compiler, err)
	}
	if raw.Format != unsafeReportFormat {
		return nil, fmt.Errorf("%s --unsafe-report wrote report format %d, and this turf reads format %d\nhint: use a turf and a turfc of the same release line",
			compiler, raw.Format, unsafeReportFormat)
	}
	r := &unsafeReport{
		Packages: map[string]unsafePackage{},
		Raw:      project.UnsafeSurface{Packages: raw.Packages, Stdlib: raw.Stdlib},
	}
	for name, msg := range raw.Packages {
		var p unsafePackage
		if err := json.Unmarshal(msg, &p); err != nil {
			return nil, fmt.Errorf("%s --unsafe-report: package '%s': %v", compiler, name, err)
		}
		r.Packages[name] = p
	}
	if err := json.Unmarshal(raw.Stdlib, &r.Stdlib); err != nil {
		return nil, fmt.Errorf("%s --unsafe-report: the stdlib's entry: %v", compiler, err)
	}
	return r, nil
}

// cmdAudit prints each package's unsafe surface as a table: what `turf graph
// --unsafe` carries as JSON, for a person to read.
func cmdAudit(args []string) int {
	nf, rest := splitNetFlags(args)
	start := ""
	files := false
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--files":
			files = true
		case a == "--project":
			if i+1 >= len(rest) {
				return usagef("audit: --project needs a directory (%s)", auditUsage)
			}
			i++
			start = rest[i]
		case strings.HasPrefix(a, "--project="):
			start = strings.TrimPrefix(a, "--project=")
		default:
			return usagef("audit: unexpected argument '%s' (%s)", a, auditUsage)
		}
	}
	g, code := graphForReport(start, nf)
	if code != 0 {
		return code
	}
	return withCompiler(func(compiler string) int {
		r, err := collectUnsafeReport(g, compiler, "turf audit")
		if err != nil {
			return failf("%v", err)
		}
		fmt.Print(renderAudit(g, r, files))
		return 0
	})
}

// graphForReport reads the project found from start (the cwd when empty) and
// resolves its graph as `turf graph` does: no lock written, no build/.
func graphForReport(start string, nf netFlags) (*project.Graph, int) {
	cwd := start
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return nil, failf("cannot determine the current directory: %v", err)
		}
	} else if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	} else {
		return nil, failf("%v", err)
	}
	m, code := loadManifestFrom(cwd)
	if code != 0 {
		return nil, code
	}
	g, _, err := resolveGraph(m, nf, nil)
	if err != nil {
		return nil, failf("%v", err)
	}
	return g, 0
}

// auditRow is one package of the table, in the order rows are printed: the
// root, its dependencies by name, then the standard library.
type auditRow struct {
	name, granted string
	p             unsafePackage
}

func auditRows(g *project.Graph, r *unsafeReport) []auditRow {
	rows := []auditRow{{g.Root.Name, "own", r.Packages[g.Root.Name]}}
	for _, n := range g.Names() {
		if n == g.Root.Name {
			continue
		}
		granted := "no"
		if g.Packages[n].AllowUnsafe {
			granted = "yes"
		}
		rows = append(rows, auditRow{n, granted, r.Packages[n]})
	}
	return append(rows, auditRow{"std", "always", r.Stdlib})
}

func libraries(l []string) string {
	if len(l) == 0 {
		return "-"
	}
	return strings.Join(l, ", ")
}

// table lays out rows of cells in columns two spaces apart; the columns in
// right are right-aligned (the counts), every other column left-aligned.
func table(rows [][]string, right map[int]bool) string {
	width := map[int]int{}
	for _, r := range rows {
		for i, c := range r {
			width[i] = max(width[i], len(c))
		}
	}
	var b strings.Builder
	for _, r := range rows {
		var line strings.Builder
		for i, c := range r {
			if i > 0 {
				line.WriteString("  ")
			}
			pad := strings.Repeat(" ", width[i]-len(c))
			if right[i] {
				line.WriteString(pad + c)
			} else {
				line.WriteString(c + pad)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}
	return b.String()
}

func renderAudit(g *project.Graph, r *unsafeReport, files bool) string {
	rows := auditRows(g, r)
	itoa := strconv.Itoa
	cells := [][]string{{"package", "granted", "files", "unsafe module", "unsafe { }", "extern fn", "C libraries"}}
	for _, row := range rows {
		p := row.p
		cells = append(cells, []string{row.name, row.granted, itoa(p.Modules), itoa(p.UnsafeModules),
			itoa(p.UnsafeBlocks), itoa(p.ExternFns), libraries(p.LinkLibraries)})
	}
	out := table(cells, map[int]bool{2: true, 3: true, 4: true, 5: true})

	if files {
		fcells := [][]string{{"package", "file", "unsafe module", "unsafe { }", "extern fn", "C libraries"}}
		for _, row := range rows {
			for _, f := range row.p.Files {
				mod := "-"
				if f.UnsafeModule {
					mod = "yes"
				}
				fcells = append(fcells, []string{row.name, f.Path, mod, itoa(f.UnsafeBlocks),
					itoa(f.ExternFns), libraries(f.LinkLibraries)})
			}
		}
		if len(fcells) > 1 {
			out += "\n" + table(fcells, map[int]bool{3: true, 4: true})
		}
	}

	var notes []string
	for _, row := range rows {
		if row.granted == "no" && row.p.UnsafeModules > 0 {
			files := "file declares"
			if row.p.UnsafeModules != 1 {
				files = "files declare"
			}
			notes = append(notes, fmt.Sprintf("note: %d %s `unsafe module;` in '%s', which [unsafe] allow in %s does not list: "+
				"the compiler rejects its raw-pointer uses (E130)",
				row.p.UnsafeModules, files, row.name, g.Root.Manifest.Path))
		}
		for _, f := range row.p.Files {
			if f.Error != "" {
				notes = append(notes, fmt.Sprintf("note: %s, %s does not lex (%s); it is counted up to there",
					row.name, f.Path, f.Error))
			}
		}
	}
	if len(notes) > 0 {
		out += "\n" + strings.Join(notes, "\n") + "\n"
	}
	return out
}

const helpAudit = `turf audit - show where the project's unsafe code is

Usage:
  turf audit [--project <dir>] [--files] [--offline] [--locked]

Run inside a project, or give --project <dir> (a directory in it).

Prints one row per package (the project's own, each dependency, and the
standard library) with how many of its source files there are, how many
declare 'unsafe module;', its 'unsafe { }' blocks, its 'extern fn'
declarations and the C libraries they name with 'extern from'. The granted
column says whether the package's 'unsafe module;' is honored: 'own' for the
project's own package, 'yes' or 'no' for a dependency as [unsafe] allow in
turf.toml says, 'always' for the standard library. A note follows for a
dependency that declares 'unsafe module;' without being granted.

--files adds a second table with each file that has something to report.

Every .tr file under a package's src/ is counted, imported or not; nothing is
compiled, and nothing is written. The counts come from the compiler (turfc
--unsafe-report, driver interface 1.12). 'turf graph --unsafe' prints the same
as JSON. --offline and --locked apply as they do to 'turf build'.
`
