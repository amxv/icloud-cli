package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amxv/icloud-cli/internal/cli"
)

func main() {
	version := flag.String("version", "dev", "version embedded in generated man-page metadata")
	check := flag.Bool("check", false, "verify committed artifacts instead of writing them")
	flag.Parse()

	assets, err := cli.GenerateReferenceAssets(*version, 0)
	if err != nil {
		fail(err)
	}

	files := map[string][]byte{
		filepath.FromSlash("src/content/docs/command-reference.md"): assets.CommandReference,
	}
	for name, data := range assets.Completions {
		files[filepath.Join("completions", name)] = data
	}
	for name, data := range assets.ManPages {
		files[filepath.Join("man", name)] = data
	}

	if *check {
		if err := checkFiles(files); err != nil {
			fail(err)
		}
		return
	}
	if err := writeFiles(files); err != nil {
		fail(err)
	}
}

func checkFiles(expected map[string][]byte) error {
	for path, want := range expected {
		got, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reference drift: %s is missing; run make references", filepath.ToSlash(path))
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("reference drift: %s differs; run make references", filepath.ToSlash(path))
		}
	}
	for _, directory := range []string{"man", "completions"} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("reference drift: %s is missing; run make references", directory)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			if _, ok := expected[path]; !ok {
				return fmt.Errorf("reference drift: unexpected generated file %s; run make references", filepath.ToSlash(path))
			}
		}
	}
	return nil
}

func writeFiles(files map[string][]byte) error {
	for _, directory := range []string{"man", "completions"} {
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		data := files[path]
		if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) && strings.HasSuffix(path, ".md") {
			data = append(append([]byte(nil), data...), '\n')
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
