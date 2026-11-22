package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/turf-lang/turf-cli/internal/upgrade"
)

// cmdUpgrade moves the install this turf belongs to onto another release by
// running that release's own installer (internal/upgrade). It only ever runs
// when asked: turf never checks for a release on its own.
func cmdUpgrade(args []string) int {
	var check bool
	var want string
	var setSmart, setPlain bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--check":
			check = true
		case a == "--smart":
			setSmart = true
		case a == "--plain":
			setPlain = true
		case a == "--version":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "turf upgrade: --version needs a release, as in --version v2.3.0")
				return exitUsage
			}
			i++
			want = args[i]
		case strings.HasPrefix(a, "--version="):
			want = strings.TrimPrefix(a, "--version=")
		case a == "-h" || a == "--help":
			fmt.Print(helpUpgrade)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "turf upgrade: unknown argument %q (run 'turf help upgrade')\n", a)
			return exitUsage
		}
	}
	if setSmart && setPlain {
		fmt.Fprintln(os.Stderr, "turf upgrade: --smart and --plain each name the one build to install; give one")
		return exitUsage
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "turf upgrade: cannot tell where this turf is: %v\n", err)
		return exitFailure
	}
	in, err := upgrade.FindInstall(exe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "turf upgrade: %v\n", err)
		return exitFailure
	}
	smart := in.Smart
	if setSmart {
		smart = true
	} else if setPlain {
		smart = false
	}

	dir := upgrade.DownloadDir(upgrade.BaseURL(), want)
	sumsData, err := upgrade.Fetch(dir + "/checksums.txt")
	if err != nil {
		if want != "" {
			fmt.Fprintf(os.Stderr, "turf upgrade: found no release v%s at %s (%v)\n",
				strings.TrimPrefix(want, "v"), upgrade.BaseURL(), err)
		} else {
			fmt.Fprintf(os.Stderr, "turf upgrade: could not read the latest release's checksums.txt: %v\n", err)
		}
		return exitFailure
	}
	sums := upgrade.Checksums(sumsData)
	target := strings.TrimPrefix(want, "v")
	if target == "" {
		target = upgrade.ReleaseVersion(sums)
	}
	if target == "" {
		fmt.Fprintf(os.Stderr, "turf upgrade: %s/checksums.txt lists no release archive\n", dir)
		return exitFailure
	}
	build := flavour(smart)
	ext := ".tar.gz"
	if strings.HasPrefix(in.Platform, "windows-") {
		ext = ".zip"
	}
	archive := "turf-v" + target + "-" + in.Platform
	if smart {
		archive += "-smart"
	}
	archive += ext
	if _, ok := sums[archive]; !ok {
		fmt.Fprintf(os.Stderr, "turf upgrade: release v%s has no %s build for %s (no %s in its checksums.txt)\n",
			target, build, in.Platform, archive)
		return exitFailure
	}

	order := upgrade.Compare(target, in.Version)
	fmt.Printf("installed: v%s, %s build, %s, in %s\n", in.Version, flavour(in.Smart), in.Platform, in.Prefix)
	if check {
		switch {
		case order > 0:
			fmt.Printf("v%s is available: run `turf upgrade`\n", target)
		case order == 0:
			fmt.Println("up to date")
		default:
			fmt.Printf("v%s is older than this install\n", target)
		}
		return 0
	}
	if order == 0 && smart == in.Smart {
		fmt.Printf("already v%s, %s build: nothing to do\n", target, build)
		return 0
	}
	if order < 0 && want == "" {
		// The latest release is older than this install (a pre-release or a
		// local build installed by hand): never downgrade without being told.
		fmt.Printf("the latest release, v%s, is older than this install; nothing to do (--version installs a given release)\n", target)
		return 0
	}

	name := upgrade.InstallerName(runtime.GOOS)
	script, err := upgrade.VerifiedInstaller(dir, name, sums)
	if err != nil {
		fmt.Fprintf(os.Stderr, "turf upgrade: %v\n", err)
		return exitFailure
	}
	tmp, err := os.MkdirTemp("", "turf-upgrade-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "turf upgrade: %v\n", err)
		return exitFailure
	}
	path := filepath.Join(tmp, name)
	if err := os.WriteFile(path, script, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "turf upgrade: %v\n", err)
		return exitFailure
	}
	fmt.Printf("installing v%s, %s build, with the release's %s (checked against its checksums.txt)\n", target, build, name)

	if runtime.GOOS == "windows" {
		// turf.exe is running from the folder the installer replaces, and a
		// running program's folder cannot be moved on Windows. The installer
		// waits for this process to end before it touches the install.
		ps := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path,
			"-Version", "v" + target, "-Prefix", in.Prefix, "-NoModifyPath",
			"-WaitForProcess", strconv.Itoa(os.Getpid())}
		if smart {
			ps = append(ps, "-Smart")
		}
		cmd := exec.Command("powershell.exe", ps...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "turf upgrade: could not start PowerShell: %v\n", err)
			return exitFailure
		}
		fmt.Println("the installer finishes once this turf has exited; its output follows")
		return 0
	}

	defer os.RemoveAll(tmp)
	sh := []string{path, "--version", "v" + target, "--prefix", in.Prefix, "--no-modify-path"}
	if smart {
		sh = append(sh, "--smart")
	}
	cmd := exec.Command("sh", sh...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Fprintln(os.Stderr, "turf upgrade: the installer stopped; the previous install is still in place")
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "turf upgrade: could not run the installer: %v\n", err)
		return exitFailure
	}
	return 0
}

func flavour(smart bool) string {
	if smart {
		return "smart"
	}
	return "plain"
}

const helpUpgrade = `turf upgrade - move this TurfLang install to another release

Usage:
  turf upgrade                   the latest release, same build (plain or smart)
  turf upgrade --check           say whether a newer release exists; install nothing
  turf upgrade --version v2.3.0  that release (an older one too)
  turf upgrade --smart | --plain switch to the smart build (the --smart model
                                 runtime) or the plain one

Works on an install made by install.sh or install.ps1 (it has a .turf-install
file): turf, turfc, the standard library and the docs move together. turf finds
the install from its own location, downloads the target release's own
installer, checks it against that release's checksums.txt, and runs it on the
same folder; the installer downloads the archive for this platform, verifies
its SHA-256, and puts the previous install back if the new one cannot be put in
place. Your PATH is left as it is, and a model in models/ stays.

A turf from Homebrew, Scoop or a source build is updated the way it was
installed; turf upgrade says how. On Windows the installer runs after turf has
exited, since a running turf.exe holds its folder.

turf never looks for a release by itself: only this command uses the network.
TURF_INSTALL_BASE_URL points at a mirror, as for the installers.
`
