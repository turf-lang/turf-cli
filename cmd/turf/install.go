package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/turf-lang/turf-cli/internal/manifest"
)

func runInstall(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: turf install <path>")
	}

	src := filepath.Clean(args[0])

	m, err := manifest.Read(filepath.Join(src, "turf.toml"))
	if err != nil {
		return fmt.Errorf("reading package manifest: %w", err)
	}

	dest := filepath.Join("turf_modules", m.Name)

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating turf_modules/: %w", err)
	}

	if err := copyDir(src, dest); err != nil {
		return fmt.Errorf("copying package: %w", err)
	}

	fmt.Printf("installed %s@%s  →  turf_modules/%s\n", m.Name, m.Version, m.Name)
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
