package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/turf-lang/turf-cli/internal/lockfile"
	"github.com/turf-lang/turf-cli/internal/resolver"
)

func runInstall(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: turf install <path>")
	}

	src := filepath.Clean(args[0])

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

	fmt.Println("written turf.lock")

	return nil
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
