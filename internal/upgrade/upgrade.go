// Package upgrade moves a TurfLang install to another release: what `turf
// upgrade` does. It never re-implements an installer. It finds the install
// the running turf belongs to, works out which release to move to, downloads
// THAT release's own installer (install.sh, install.ps1), checks it against
// the release's checksums.txt, and runs it on the same prefix with the same
// build (plain or smart). The installer then does what it always does:
// detect the platform, download the archive, verify its SHA-256, move the old
// files aside and restore them if the new ones cannot be put in place, and
// keep any model in models/.
package upgrade

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is where releases are published. TURF_INSTALL_BASE_URL
// points at a mirror instead, or a directory laid out the same way
// (`latest/download/...`, `download/<tag>/...`; file:// works), exactly as
// the installers read it.
const DefaultBaseURL = "https://github.com/turf-lang/turf-lang/releases"

// BaseURL is DefaultBaseURL unless TURF_INSTALL_BASE_URL says otherwise.
func BaseURL() string {
	if v := strings.TrimRight(os.Getenv("TURF_INSTALL_BASE_URL"), "/"); v != "" {
		return v
	}
	return DefaultBaseURL
}

// Install is the TurfLang install a turf binary belongs to.
type Install struct {
	Prefix   string // the directory install.sh / install.ps1 installed into
	Version  string // "2.3.0", from the archive it was installed from
	Platform string // "linux-x86_64"
	Smart    bool   // the smart build (the --smart model runtime)
}

// NotManaged is the error for a turf that no installer manages, with what to
// do instead.
type NotManaged struct{ Msg, Hint string }

func (e *NotManaged) Error() string { return e.Msg + "\nhint: " + e.Hint }

// archiveRe reads a release archive's name: turf-v<version>-<platform>[-smart].<ext>.
var archiveRe = regexp.MustCompile(`^turf-v([0-9][0-9A-Za-z.]*(?:-[a-z]+[0-9A-Za-z.]*)?)-((?:linux|macos|windows)-[a-z0-9_]+)(-smart)?\.(?:tar\.gz|zip)$`)

// ParseArchive splits a release archive's file name.
func ParseArchive(name string) (version, platform string, smart, ok bool) {
	m := archiveRe.FindStringSubmatch(name)
	if m == nil {
		return "", "", false, false
	}
	return m[1], m[2], m[3] != "", true
}

// FindInstall finds the install that exe (the running turf, symlinks
// resolved) belongs to: <prefix>/bin/turf with <prefix>/.turf-install, the
// manifest the installers write. A turf installed some other way is a
// NotManaged error that says how to update it instead.
func FindInstall(exe string) (Install, error) {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	bin := filepath.Dir(exe)
	prefix := filepath.Dir(bin)
	manifest := filepath.Join(prefix, ".turf-install")
	data, err := os.ReadFile(manifest)
	if err != nil {
		return Install{}, notManaged(exe, prefix)
	}
	in := Install{Prefix: prefix}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, _ := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if k == "archive" {
			ver, plat, smart, ok := ParseArchive(v)
			if !ok {
				return Install{}, &NotManaged{
					Msg:  fmt.Sprintf("%s names an archive turf does not recognise (%q)", manifest, v),
					Hint: "reinstall with install.sh or install.ps1, which rewrites it",
				}
			}
			in.Version, in.Platform, in.Smart = ver, plat, smart
		}
	}
	if in.Version == "" {
		return Install{}, &NotManaged{
			Msg:  manifest + " does not say which archive was installed",
			Hint: "reinstall with install.sh or install.ps1, which rewrites it",
		}
	}
	return in, nil
}

func notManaged(exe, prefix string) error {
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/"):
		return &NotManaged{Msg: "this turf (" + exe + ") was installed by Homebrew",
			Hint: "run `brew upgrade turf`"}
	case strings.Contains(strings.ToLower(p), "/scoop/apps/"):
		return &NotManaged{Msg: "this turf (" + exe + ") was installed by Scoop",
			Hint: "run `scoop update turf`"}
	case isFile(filepath.Join(prefix, "CMakeLists.txt")) || filepath.Base(filepath.Dir(exe)) == "build":
		return &NotManaged{Msg: "this turf (" + exe + ") was built from source",
			Hint: "update the checkout (`git pull`) and build it again (`./scripts/cmake_build.sh`)"}
	}
	return &NotManaged{
		Msg:  "this turf (" + exe + ") was not installed by install.sh or install.ps1 (there is no " + filepath.Join(prefix, ".turf-install") + ")",
		Hint: "install a release with the installer, which `turf upgrade` can then keep up to date: see https://github.com/turf-lang/turf-lang#install",
	}
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Fetch reads url: http(s), or a local file for a file:// mirror.
func Fetch(url string) ([]byte, error) {
	if path, ok := strings.CutPrefix(url, "file://"); ok {
		return os.ReadFile(path)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Checksums parses a release's checksums.txt: file name -> SHA-256 (hex).
func Checksums(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// DownloadDir is where release <version>'s files are ("" = the latest).
func DownloadDir(base, version string) string {
	if version == "" {
		return base + "/latest/download"
	}
	return base + "/download/v" + strings.TrimPrefix(version, "v")
}

// ReleaseVersion reads the version of the release whose checksums.txt this
// is, from the archives it lists (they all carry it). Empty if none does.
func ReleaseVersion(sums map[string]string) string {
	best := ""
	for name := range sums {
		if v, _, _, ok := ParseArchive(name); ok && (best == "" || Compare(v, best) > 0) {
			best = v
		}
	}
	return best
}

// Compare orders two versions numerically, part by part ("2.10.0" > "2.9.1");
// a pre-release ("2.3.0-rc1") comes before its release.
func Compare(a, b string) int {
	ac, ap, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	bc, bp, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	as, bs := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := 0, 0
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	}
	return 1
}

// InstallerName is the installer for an OS ("windows" or anything else).
func InstallerName(goos string) string {
	if goos == "windows" {
		return "install.ps1"
	}
	return "install.sh"
}

// VerifiedInstaller downloads release dir's installer and checks it against
// the release's checksums. A release whose checksums.txt does not list its
// installer (before 2.3.0) cannot be checked, and is refused.
func VerifiedInstaller(dir, name string, sums map[string]string) ([]byte, error) {
	want, ok := sums[name]
	if !ok {
		return nil, fmt.Errorf("this release's checksums.txt does not cover %s, so turf cannot check it; install that release with the installer directly", name)
	}
	data, err := Fetch(dir + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", name, err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s does not match its SHA-256 in checksums.txt; nothing was installed", name)
	}
	return data, nil
}
