package main

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/turf-lang/turf-cli/internal/archive"
	"github.com/turf-lang/turf-cli/internal/checksum"
	"github.com/turf-lang/turf-cli/internal/manifest"
)

func runPublish(args []string) error {
	m, err := manifest.Read("turf.toml")
	if err != nil {
		return fmt.Errorf("reading manifest: %w", err)
	}

	filename := fmt.Sprintf("%s-%s.tar.gz", m.Name, m.Version)

	if err := archive.Pack(".", filename); err != nil {
		return fmt.Errorf("packing archive: %w", err)
	}

	absPath, err := filepath.Abs(filename)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	fmt.Printf("packed %s@%s  →  %s\n", m.Name, m.Version, absPath)

	hash, err := checksum.Hash(filename)
	if err != nil {
		return fmt.Errorf("hashing archive: %w", err)
	}

	if err := pushToRegistry(filename, m.Name, m.Version, hash); err != nil {
		return fmt.Errorf("pushing to registry: %w", err)
	}

	fmt.Printf("sha256: %s\n", hash)
	return nil
}

func pushToRegistry(filename, name, version, hash string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	writer.WriteField("name", name)
	writer.WriteField("version", version)

	part, err := writer.CreateFormFile("archive", filename)
	if err != nil {
		return err
	}

	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(part, f); err != nil {
		return err
	}

	writer.Close()

	resp, err := http.Post(
		"http://localhost:8080/publish",
		writer.FormDataContentType(),
		&body,
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Print(string(respBody))
	return nil
}
