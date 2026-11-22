package upgrade

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArchive(t *testing.T) {
	for _, c := range []struct {
		name, ver, plat string
		smart, ok       bool
	}{
		{"turf-v2.3.0-linux-x86_64.tar.gz", "2.3.0", "linux-x86_64", false, true},
		{"turf-v2.3.0-linux-x86_64-smart.tar.gz", "2.3.0", "linux-x86_64", true, true},
		{"turf-v2.3.0-macos-arm64.tar.gz", "2.3.0", "macos-arm64", false, true},
		{"turf-v2.3.0-windows-x86_64-smart.zip", "2.3.0", "windows-x86_64", true, true},
		{"turf-v2.4.0-rc1-linux-arm64.tar.gz", "2.4.0-rc1", "linux-arm64", false, true},
		{"install.sh", "", "", false, false},
		{"turf-v2.3.0-linux-x86_64.tar.gz.sha256", "", "", false, false},
	} {
		v, p, s, ok := ParseArchive(c.name)
		if v != c.ver || p != c.plat || s != c.smart || ok != c.ok {
			t.Errorf("ParseArchive(%q) = %q %q %v %v, want %q %q %v %v", c.name, v, p, s, ok, c.ver, c.plat, c.smart, c.ok)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2.3.0", "2.3.0", 0},
		{"v2.3.0", "2.3.0", 0},
		{"2.10.0", "2.9.1", 1},
		{"2.3.0", "2.3.1", -1},
		{"2.3.0-rc1", "2.3.0", -1},
		{"2.3.0", "2.3.0-rc1", 1},
		{"2.3.0-rc2", "2.3.0-rc1", 1},
		{"3", "2.99.99", 1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestReleaseVersionAndChecksums(t *testing.T) {
	sums := Checksums([]byte("aa  turf-v2.3.0-linux-x86_64.tar.gz\nbb *turf-v2.3.0-windows-x86_64.zip\ncc  install.sh\n\n"))
	if sums["turf-v2.3.0-windows-x86_64.zip"] != "bb" || sums["install.sh"] != "cc" {
		t.Fatalf("Checksums: %v", sums)
	}
	if v := ReleaseVersion(sums); v != "2.3.0" {
		t.Errorf("ReleaseVersion = %q", v)
	}
	if v := ReleaseVersion(map[string]string{"install.sh": "x"}); v != "" {
		t.Errorf("ReleaseVersion with no archive = %q", v)
	}
}

func TestDownloadDir(t *testing.T) {
	if got := DownloadDir("B", ""); got != "B/latest/download" {
		t.Error(got)
	}
	if got := DownloadDir("B", "v2.3.0"); got != "B/download/v2.3.0" {
		t.Error(got)
	}
	if got := DownloadDir("B", "2.3.0"); got != "B/download/v2.3.0" {
		t.Error(got)
	}
}

// prefix makes <dir>/bin/turf and, when manifest is not "", .turf-install.
func prefix(t *testing.T, manifest string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "bin", "turf")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, ".turf-install"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, exe
}

func TestFindInstall(t *testing.T) {
	dir, exe := prefix(t, "archive=turf-v2.2.0-linux-x86_64-smart.tar.gz\ncompiler=turfc 2.2.0\n")
	in, err := FindInstall(exe)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if in.Prefix != real || in.Version != "2.2.0" || in.Platform != "linux-x86_64" || !in.Smart {
		t.Errorf("FindInstall = %+v", in)
	}

	// A symlink to the installed turf (~/.local/bin/turf) finds the install.
	link := filepath.Join(t.TempDir(), "turf")
	if err := os.Symlink(exe, link); err == nil {
		if in2, err := FindInstall(link); err != nil || in2.Version != "2.2.0" {
			t.Errorf("FindInstall via symlink = %+v, %v", in2, err)
		}
	}
}

func TestFindInstallNotManaged(t *testing.T) {
	_, exe := prefix(t, "")
	_, err := FindInstall(exe)
	nm, ok := err.(*NotManaged)
	if !ok || !strings.Contains(nm.Msg, "was not installed by install.sh") {
		t.Fatalf("no manifest: %v", err)
	}

	// A source checkout: build/turf beside the CMakeLists.txt's directory.
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "build"), 0o755)
	os.WriteFile(filepath.Join(src, "CMakeLists.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(src, "build", "turf"), nil, 0o755)
	_, err = FindInstall(filepath.Join(src, "build", "turf"))
	if nm, ok := err.(*NotManaged); !ok || !strings.Contains(nm.Hint, "git pull") {
		t.Errorf("source build: %v", err)
	}

	if err := notManaged("/opt/homebrew/Cellar/turf/2.2.0/bin/turf", "/x"); !strings.Contains(err.Error(), "brew upgrade turf") {
		t.Errorf("homebrew: %v", err)
	}
	if err := notManaged(`C:/Users/a/scoop/apps/turf/current/bin/turf.exe`, "/x"); !strings.Contains(err.Error(), "scoop update turf") {
		t.Errorf("scoop: %v", err)
	}

	_, exe = prefix(t, "compiler=turfc 2.2.0\n")
	if _, err := FindInstall(exe); err == nil {
		t.Error("a manifest with no archive line was accepted")
	}
}

func TestVerifiedInstaller(t *testing.T) {
	dir := t.TempDir()
	script := []byte("#!/bin/sh\necho hi\n")
	os.WriteFile(filepath.Join(dir, "install.sh"), script, 0o644)
	sum := sha256.Sum256(script)
	good := map[string]string{"install.sh": hex.EncodeToString(sum[:])}
	if got, err := VerifiedInstaller("file://"+dir, "install.sh", good); err != nil || string(got) != string(script) {
		t.Fatalf("good installer: %v", err)
	}
	if _, err := VerifiedInstaller("file://"+dir, "install.sh", map[string]string{"install.sh": strings.Repeat("0", 64)}); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Errorf("tampered installer: %v", err)
	}
	if _, err := VerifiedInstaller("file://"+dir, "install.sh", map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "does not cover") {
		t.Errorf("unlisted installer: %v", err)
	}
}
