package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/turf-lang/turf-cli/internal/archive"
	"github.com/turf-lang/turf-cli/internal/checksum"
	"github.com/turf-lang/turf-cli/internal/lockfile"
	"github.com/turf-lang/turf-cli/internal/manifest"
	"github.com/turf-lang/turf-cli/internal/resolver"
)

func runInstall(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: turf install <path or package name>")
	}

	src := args[0]

	if isLocalPath(src) {
		return installLocal(src)
	}

	return installFromRegistry(src)
}

func isLocalPath(s string) bool {
	return strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "/")
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func installLocal(src string) error {
	src = filepath.Clean(src)

	visited := map[string]*resolver.Node{}
	root, err := resolver.Build(src, visited)
	if err != nil {
		return fmt.Errorf("resolving dependencies: %w", err)
	}

	order := []*resolver.Node{}
	resolver.Resolve(root, map[string]bool{}, &order)

	for _, node := range order {
		dest := filepath.Join("turf_modules", node.Manifest.Name)

		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("creating turf_modules/: %w", err)
		}

		pkgSrc := filepath.Join(filepath.Dir(src), node.Manifest.Name)
		if node.Manifest.Name == root.Manifest.Name {
			pkgSrc = src
		}

		if err := copyDir(pkgSrc, dest); err != nil {
			return fmt.Errorf("installing %s: %w", node.Manifest.Name, err)
		}

		fmt.Printf("installed %s@%s  →  turf_modules/%s\n", node.Manifest.Name, node.Manifest.Version, node.Manifest.Name)
	}

	lf := &lockfile.Lockfile{}
	for _, node := range order {
		lf.Packages = append(lf.Packages, lockfile.Entry{
			Name:    node.Manifest.Name,
			Version: node.Manifest.Version,
		})
	}

	if err := lockfile.Write("turf.lock", lf); err != nil {
		return fmt.Errorf("writing lockfile: %w", err)
	}

	fmt.Println("wrote turf.lock")
	return nil
}

func installFromRegistry(name string) error {
	meta, err := fetchMeta(name)
	if err != nil {
		return err
	}

	version, hash, err := latestVersion(meta)
	if err != nil {
		return err
	}

	fmt.Printf("resolving %s@%s\n", name, version)

	if err := downloadAndInstall(name, version, hash); err != nil {
		return err
	}

	return nil
}

type registryMeta struct {
	Name     string            `json:"name"`
	Versions map[string]string `json:"versions"`
}

func fetchMeta(name string) (*registryMeta, error) {
	resp, err := http.Get("http://localhost:8080/package/" + name)
	if err != nil {
		return nil, fmt.Errorf("contacting registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("package %q not found in registry", name)
	}

	var meta registryMeta
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("parsing registry response: %w", err)
	}

	return &meta, nil
}

func latestVersion(meta *registryMeta) (version, hash string, err error) {
	for v, h := range meta.Versions {
		version = v
		hash = h
	}
	if version == "" {
		return "", "", fmt.Errorf("no versions available for %q", meta.Name)
	}
	return version, hash, nil
}

func downloadAndInstall(name, version, expectedHash string) error {
	if version == "" {
		meta, err := fetchMeta(name)
		if err != nil {
			return err
		}
		var err2 error
		version, expectedHash, err2 = latestVersion(meta)
		if err2 != nil {
			return err2
		}
	}

	url := fmt.Sprintf("http://localhost:8080/package/%s/%s/download", name, version)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("downloading %s@%s: %w", name, version, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned %d for %s@%s", resp.StatusCode, name, version)
	}

	archivePath := fmt.Sprintf("/tmp/%s-%s.tar.gz", name, version)

	out, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("saving archive: %w", err)
	}
	out.Close()

	hash, err := checksum.Hash(archivePath)
	if err != nil {
		return err
	}

	if hash != expectedHash {
		return fmt.Errorf("checksum mismatch for %s@%s", name, version)
	}

	dest := filepath.Join("turf_modules", name)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	if err := archive.Unpack(archivePath, dest); err != nil {
		return fmt.Errorf("extracting %s: %w", name, err)
	}

	fmt.Printf("installed %s@%s  →  turf_modules/%s\n", name, version, name)

	m, err := manifest.Read(filepath.Join(dest, "turf.toml"))
	if err != nil {
		return fmt.Errorf("reading installed manifest: %w", err)
	}

	for dep := range m.Dependencies {
		if err := downloadAndInstall(dep, "", ""); err != nil {
			return err
		}
	}

	return nil
}
