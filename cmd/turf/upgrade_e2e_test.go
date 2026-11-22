package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUpgradeEndToEnd builds turf into a fake install and upgrades it from a
// file:// release mirror whose install.sh only prints its arguments.
func TestUpgradeEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows path hands off to install.ps1 after turf exits")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain to build turf with")
	}
	root := t.TempDir()
	pfx := filepath.Join(root, "pfx")
	os.MkdirAll(filepath.Join(pfx, "bin"), 0o755)
	turf := filepath.Join(pfx, "bin", "turf")
	if out, err := exec.Command("go", "build", "-o", turf, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	manifest := filepath.Join(pfx, ".turf-install")
	setInstalled := func(archive string) {
		os.WriteFile(manifest, []byte("archive="+archive+"\ncompiler=turfc\n"), 0o644)
	}

	mirror := filepath.Join(root, "mirror")
	installer := "#!/bin/sh\necho \"INSTALLER $*\"\n"
	var sums strings.Builder
	files := map[string]string{
		"turf-v2.4.0-linux-x86_64.tar.gz":       "",
		"turf-v2.4.0-linux-x86_64-smart.tar.gz": "",
		"install.sh":                            installer,
	}
	for name, body := range files {
		h := sha256.Sum256([]byte(body))
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	for _, d := range []string{"download/v2.4.0", "latest/download"} {
		dir := filepath.Join(mirror, d)
		os.MkdirAll(dir, 0o755)
		for name, body := range files {
			os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
		}
		os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(sums.String()), 0o644)
	}

	run := func(args ...string) (string, int) {
		cmd := exec.Command(turf, append([]string{"upgrade"}, args...)...)
		cmd.Env = append(os.Environ(), "TURF_INSTALL_BASE_URL=file://"+mirror)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return string(out), code
	}
	expect := func(what, out string, code, wantCode int, wants ...string) {
		t.Helper()
		if code != wantCode {
			t.Errorf("%s: exit %d, want %d\n%s", what, code, wantCode, out)
		}
		for _, w := range wants {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output lacks %q\n%s", what, w, out)
			}
		}
	}

	setInstalled("turf-v2.3.0-linux-x86_64-smart.tar.gz")
	out, code := run("--check")
	expect("check", out, code, 0, "v2.4.0 is available")
	out, code = run()
	expect("upgrade keeps the smart build", out, code, 0,
		"INSTALLER --version v2.4.0 --prefix "+pfx+" --no-modify-path --smart")
	out, code = run("--plain")
	expect("--plain", out, code, 0, "INSTALLER --version v2.4.0 --prefix "+pfx+" --no-modify-path\n")

	setInstalled("turf-v2.4.0-linux-x86_64-smart.tar.gz")
	out, code = run()
	expect("up to date", out, code, 0, "nothing to do")

	setInstalled("turf-v2.3.0-macos-arm64.tar.gz")
	out, code = run()
	expect("no archive for the platform", out, code, 1, "has no plain build for macos-arm64")

	setInstalled("turf-v2.3.0-linux-x86_64.tar.gz")
	out, code = run("--version", "v9.0.0")
	expect("unknown release", out, code, 1, "found no release v9.0.0")

	os.WriteFile(filepath.Join(mirror, "latest/download/install.sh"), []byte(installer+"echo tampered\n"), 0o644)
	out, code = run()
	expect("tampered installer", out, code, 1, "does not match its SHA-256")
	if strings.Contains(out, "INSTALLER") {
		t.Errorf("a tampered installer ran:\n%s", out)
	}

	os.Remove(manifest)
	out, code = run()
	expect("not installed by an installer", out, code, 1, "was not installed by install.sh or install.ps1")
}
