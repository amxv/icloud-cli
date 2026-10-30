//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/amxv/icloud-cli/internal/cli"
)

func main() {
	outputs := []string{"schemas/v1", "docs/public/schemas/v1"}
	if len(os.Args) == 2 {
		outputs = []string{os.Args[1]}
	} else if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/schema-gen.go [output-directory]")
		os.Exit(2)
	}
	documents, err := cli.GeneratedSchemas()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	keys := make([]string, 0, len(documents))
	for filename := range documents {
		keys = append(keys, filename)
	}
	sort.Strings(keys)
	for _, output := range outputs {
		if err := os.MkdirAll(output, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		entries, err := os.ReadDir(output)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".schema.json") {
				continue
			}
			if err := os.Remove(filepath.Join(output, entry.Name())); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		for _, filename := range keys {
			if err := os.WriteFile(filepath.Join(output, filename), documents[filename], 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
}
