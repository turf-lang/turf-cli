package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/turf-lang/turf-cli/internal/driver"
	"github.com/turf-lang/turf-cli/internal/version"
)

// The report a turfc of interface 1.12 writes for unsafeProject: the root, a
// granted dependency with an `unsafe module;` file and `extern from "z"`, an
// ungranted one that declares `unsafe module;`, and the standard library.
const fakeUnsafeReport = `{"format":1,"packages":{` +
	`"app":{"allow_unsafe":true,"extern_fns":0,"files":[],"link_libraries":[],"modules":1,"unsafe_blocks":0,"unsafe_modules":0},` +
	`"ffi":{"allow_unsafe":true,"extern_fns":2,"files":[{"extern_fns":2,"link_libraries":["z"],"path":"ffi.tr","unsafe_blocks":1,"unsafe_module":true}],"link_libraries":["z"],"modules":1,"unsafe_blocks":1,"unsafe_modules":1},` +
	`"raw":{"allow_unsafe":false,"extern_fns":0,"files":[{"extern_fns":0,"link_libraries":[],"path":"raw.tr","unsafe_blocks":0,"unsafe_module":true,"error":"line 3: Unterminated string"}],"link_libraries":[],"modules":1,"unsafe_blocks":0,"unsafe_modules":1}},` +
	`"stdlib":{"allow_unsafe":true,"extern_fns":75,"files":[{"extern_fns":9,"link_libraries":[],"path":"mem/mem.tr","unsafe_blocks":0,"unsafe_module":true}],"link_libraries":["m"],"modules":25,"unsafe_blocks":0,"unsafe_modules":12}}`

// unsafeProject writes app -> ffi, raw with `[unsafe] allow = ["ffi"]`, and a
// fake turfc that answers --version with interface iface and --unsafe-report
// with fakeUnsafeReport. The graph it was handed is copied to seen-graph.json
// in the returned directory. It returns that directory and the project root.
func unsafeProject(t *testing.T, iface string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell for the fake compiler")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app", "turf.toml"),
		"[package]\nname = \"app\"\nversion = \"0.1.0\"\nturf = \"2.0\"\n\n[dependencies]\n"+
			"ffi = { path = \"../ffi\" }\nraw = { path = \"../raw\" }\n\n[unsafe]\nallow = [\"ffi\"]\n")
	writeFile(t, filepath.Join(dir, "app", "src", "main.tr"), "fn main(): int { return 0; }\n")
	for _, n := range []string{"ffi", "raw"} {
		writeFile(t, filepath.Join(dir, n, "turf.toml"),
			"[package]\nname = \""+n+"\"\nversion = \"0.1.0\"\nturf = \"2.0\"\n")
		writeFile(t, filepath.Join(dir, n, "src", n+".tr"), "unsafe module;\n")
	}
	seen := filepath.Join(dir, "seen-graph.json")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --version ]; then printf 'turfc 2.2.0\\ninterface " + iface + "\\n'; exit 0; fi\n" +
		"if [ \"$1\" = --unsafe-report ] && [ \"$2\" = --package-graph ] && [ -f \"$3\" ]; then\n" +
		"  cp \"$3\" '" + seen + "'\n" +
		"  printf '%s\\n' '" + fakeUnsafeReport + "'\n  exit 0\nfi\n" +
		"echo \"fake turfc: unexpected arguments: $*\" >&2\nexit 2\n"
	turfc := filepath.Join(dir, driver.CompilerFile)
	if err := os.WriteFile(turfc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TURF_COMPILER", turfc)
	return dir, filepath.Join(dir, "app")
}

// stdoutOf runs fn and returns its status and what it printed on stdout.
func stdoutOf(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	code := fn()
	w.Close()
	os.Stdout = old
	return code, <-done
}

func TestUnsafeInterfaceError(t *testing.T) {
	need := version.RequiredForUnsafeReport
	cases := []struct {
		name string
		got  version.Interface
		bad  bool
	}{
		{"exactly the required", need, false},
		{"a later minor", version.Interface{Major: need.Major, Minor: need.Minor + 3}, false},
		{"an earlier minor", version.Interface{Major: need.Major, Minor: need.Minor - 1}, true},
		{"another major", version.Interface{Major: need.Major + 1, Minor: 0}, true},
	}
	for _, tc := range cases {
		err := unsafeInterfaceError(driver.CompilerInfo{Release: "2.1.1", Interface: tc.got}, "/x/turfc", "turf audit")
		if (err != nil) != tc.bad {
			t.Errorf("%s: error = %v, want error: %v", tc.name, err, tc.bad)
		}
		if err != nil && (!strings.Contains(err.Error(), "turf audit") || !strings.Contains(err.Error(), "upgrade turfc")) {
			t.Errorf("%s: the message should name the command and say what to do: %v", tc.name, err)
		}
	}
}

// `turf audit` is a table of what the compiler counted, one row per package in
// a fixed order (the root, its dependencies by name, the standard library),
// with what the root's [unsafe] allow grants, a note for an ungranted
// dependency that declares `unsafe module;`, and one for a file that does not
// lex. The compiler is handed the graph `turf build` would write.
func TestAuditPrintsEachPackagesUnsafeSurface(t *testing.T) {
	dir, app := unsafeProject(t, "1.12")
	t.Chdir(filepath.Join(app, "src"))

	code, out := stdoutOf(t, func() int { return cmdAudit(nil) })
	if code != 0 {
		t.Fatalf("exit status %d, output:\n%s", code, out)
	}
	want := "package  granted  files  unsafe module  unsafe { }  extern fn  C libraries\n" +
		"app      own          1              0           0          0  -\n" +
		"ffi      yes          1              1           1          2  z\n" +
		"raw      no           1              1           0          0  -\n" +
		"std      always      25             12           0         75  m\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("table:\n%s\nwant it to start with:\n%s", out, want)
	}
	for _, note := range []string{
		"note: 1 file declares `unsafe module;` in 'raw', which [unsafe] allow in ",
		"note: raw, raw.tr does not lex (line 3: Unterminated string); it is counted up to there",
	} {
		if !strings.Contains(out, note) {
			t.Errorf("output lacks %q:\n%s", note, out)
		}
	}
	if strings.Contains(out, "file  ") {
		t.Errorf("the per-file table is only for --files:\n%s", out)
	}

	var g struct {
		Root     string
		Packages map[string]struct {
			AllowUnsafe bool `json:"allow_unsafe"`
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "seen-graph.json"))
	if err != nil {
		t.Fatalf("the compiler was not handed a graph: %v", err)
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	if g.Root != "app" || !g.Packages["ffi"].AllowUnsafe || g.Packages["raw"].AllowUnsafe {
		t.Errorf("graph handed to the compiler: %s", data)
	}
	// Nothing in the project is written.
	for _, p := range []string{"turf.lock", "build"} {
		if _, err := os.Stat(filepath.Join(app, p)); err == nil {
			t.Errorf("turf audit wrote %s", p)
		}
	}

	code, out = stdoutOf(t, func() int { return cmdAudit([]string{"--files"}) })
	if code != 0 {
		t.Fatalf("--files: exit status %d", code)
	}
	for _, row := range []string{
		"package  file        unsafe module  unsafe { }  extern fn  C libraries\n",
		"ffi      ffi.tr      yes                     1          2  z\n",
		"raw      raw.tr      yes                     0          0  -\n",
		"std      mem/mem.tr  yes                     0          9  -\n",
	} {
		if !strings.Contains(out, row) {
			t.Errorf("--files output lacks %q:\n%s", row, out)
		}
	}
}

// `turf graph --unsafe` is the graph with the compiler's counts in it; plain
// `turf graph` is unchanged, since turfc reads that one back.
func TestGraphUnsafeCarriesTheCounts(t *testing.T) {
	_, app := unsafeProject(t, "1.12")
	t.Chdir(app)

	code, out := graphOf(t, "--unsafe")
	if code != 0 {
		t.Fatalf("exit status %d, output: %s", code, out)
	}
	var g struct {
		Packages map[string]struct {
			AllowUnsafe bool `json:"allow_unsafe"`
			Unsafe      *struct {
				UnsafeModules int      `json:"unsafe_modules"`
				ExternFns     int      `json:"extern_fns"`
				LinkLibraries []string `json:"link_libraries"`
			}
		}
		Stdlib *struct {
			Unsafe struct {
				UnsafeModules int `json:"unsafe_modules"`
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	ffi, raw := g.Packages["ffi"], g.Packages["raw"]
	if ffi.Unsafe == nil || ffi.Unsafe.ExternFns != 2 || len(ffi.Unsafe.LinkLibraries) != 1 || !ffi.AllowUnsafe {
		t.Errorf("ffi: %+v\n%s", ffi, out)
	}
	if raw.Unsafe == nil || raw.Unsafe.UnsafeModules != 1 || raw.AllowUnsafe {
		t.Errorf("raw: %+v\n%s", raw, out)
	}
	if g.Packages["app"].Unsafe == nil {
		t.Errorf("app has no unsafe object:\n%s", out)
	}
	if g.Stdlib == nil || g.Stdlib.Unsafe.UnsafeModules != 12 {
		t.Errorf("stdlib: %+v\n%s", g.Stdlib, out)
	}

	code, plain := graphOf(t)
	if code != 0 {
		t.Fatalf("plain graph: exit status %d", code)
	}
	if strings.Contains(plain, `"unsafe"`) || strings.Contains(plain, `"stdlib"`) {
		t.Errorf("plain `turf graph` must stay a valid compiler input:\n%s", plain)
	}
}

// A compiler older than interface 1.12 has no --unsafe-report: both commands
// say so, and plain `turf graph` still works without asking it anything.
func TestUnsafeReportNeedsInterface112(t *testing.T) {
	_, app := unsafeProject(t, "1.11")
	t.Chdir(app)
	if code, _ := stdoutOf(t, func() int { return cmdAudit(nil) }); code != exitFailure {
		t.Errorf("turf audit with interface 1.11: exit status %d, want %d", code, exitFailure)
	}
	if code, _ := graphOf(t, "--unsafe"); code != exitFailure {
		t.Errorf("turf graph --unsafe with interface 1.11: exit status %d, want %d", code, exitFailure)
	}
	if code, _ := graphOf(t); code != 0 {
		t.Errorf("plain turf graph with interface 1.11: exit status %d, want 0", code)
	}
}

func TestParseUnsafeReportRejectsAnotherFormat(t *testing.T) {
	_, err := parseUnsafeReport([]byte(`{"format":2,"packages":{},"stdlib":{}}`), "/x/turfc")
	if err == nil || !strings.Contains(err.Error(), "format 2") {
		t.Errorf("a report of another format: %v", err)
	}
	if _, err := parseUnsafeReport([]byte("not json"), "/x/turfc"); err == nil {
		t.Error("output that is not JSON was accepted")
	}
	if r, err := parseUnsafeReport([]byte(fakeUnsafeReport), "/x/turfc"); err != nil || r.Stdlib.ExternFns != 75 {
		t.Errorf("the fake report: %+v, %v", r, err)
	}
}

func TestAuditUsage(t *testing.T) {
	if code := cmdAudit([]string{"--nope"}); code != exitUsage {
		t.Errorf("an unknown argument: exit status %d, want %d", code, exitUsage)
	}
	if code := cmdAudit([]string{"--project"}); code != exitUsage {
		t.Errorf("--project with no directory: exit status %d, want %d", code, exitUsage)
	}
}
