package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const (
	MaximumJSONInputBytes  = 1 << 20
	MaximumBulkItems       = 100
	writeCommandAnnotation = "icloud.write"
	idempotencyKeyHelp     = "Reuse the original operation when the same normalized request and idempotency key are repeated"
)

type jsonInputOptions struct {
	Path string
}

func bindJSONInput(command *cobra.Command, options *jsonInputOptions) {
	if command.Annotations == nil {
		command.Annotations = map[string]string{}
	}
	command.Annotations[writeCommandAnnotation] = "true"
	command.Flags().StringVar(&options.Path, "input-json", "", "Read one strict JSON write request from a file, or - for stdin; cannot be mixed with equivalent write flags or positional write arguments")
}

func exactArgsOrJSONInput(options *jsonInputOptions, count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(options.Path) != "" {
			if len(args) != 0 {
				return NewAppError(ErrorUsage, "positional write arguments cannot be combined with --input-json", false)
			}
			return nil
		}
		return cobra.ExactArgs(count)(cmd, args)
	}
}

func maximumArgsOrJSONInput(options *jsonInputOptions, count int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(options.Path) != "" {
			if len(args) != 0 {
				return NewAppError(ErrorUsage, "positional write arguments cannot be combined with --input-json", false)
			}
			return nil
		}
		return cobra.MaximumNArgs(count)(cmd, args)
	}
}

func readJSONInput(command *cobra.Command, state *executionState, options jsonInputOptions, target any, equivalentFlags ...string) (bool, error) {
	path := strings.TrimSpace(options.Path)
	if path == "" {
		return false, nil
	}
	for _, name := range equivalentFlags {
		if command.Flags().Changed(name) {
			return false, NewAppError(ErrorUsage, fmt.Sprintf("--%s cannot be combined with --input-json", name), false)
		}
	}
	var reader io.Reader
	var closer io.Closer
	if path == "-" {
		reader = state.stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return false, WrapAppError(ErrorConfiguration, "cannot open --input-json file", false, err)
		}
		reader, closer = file, file
	}
	if closer != nil {
		defer closer.Close()
	}
	contents, err := io.ReadAll(io.LimitReader(reader, MaximumJSONInputBytes+1))
	if err != nil {
		return false, WrapAppError(ErrorConfiguration, "cannot read --input-json", false, err)
	}
	if len(contents) > MaximumJSONInputBytes {
		return false, NewAppError(ErrorSafety, fmt.Sprintf("--input-json exceeds %d bytes", MaximumJSONInputBytes), false)
	}
	if len(bytes.TrimSpace(contents)) == 0 {
		return false, NewAppError(ErrorUsage, "--input-json is empty", false)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, WrapAppError(ErrorUsage, "invalid --input-json request", false, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return false, NewAppError(ErrorUsage, "--input-json must contain exactly one JSON object", false)
		}
		return false, WrapAppError(ErrorUsage, "invalid trailing --input-json data", false, err)
	}
	return true, nil
}

func readJSONLines(state *executionState, path string, target func(int, []byte) error) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	var reader io.Reader
	var closer io.Closer
	if path == "-" {
		reader = state.stdin
	} else {
		file, err := os.Open(path)
		if err != nil {
			return WrapAppError(ErrorConfiguration, "cannot open JSONL input file", false, err)
		}
		reader, closer = file, file
	}
	if closer != nil {
		defer closer.Close()
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), MaximumJSONInputBytes)
	line := 0
	items := 0
	for scanner.Scan() {
		line++
		value := bytes.TrimSpace(scanner.Bytes())
		if len(value) == 0 {
			continue
		}
		items++
		if items > MaximumBulkItems {
			return NewAppError(ErrorSafety, fmt.Sprintf("bulk input exceeds %d items", MaximumBulkItems), false)
		}
		if err := target(line, append([]byte(nil), value...)); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return WrapAppError(ErrorConfiguration, "cannot read JSONL input", false, err)
	}
	if items == 0 {
		return NewAppError(ErrorUsage, "JSONL input contains no items", false)
	}
	return nil
}

func decodeStrictJSONLine(line int, value []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return WrapAppError(ErrorUsage, fmt.Sprintf("invalid JSONL item at line %d", line), false, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return NewAppError(ErrorUsage, fmt.Sprintf("JSONL line %d must contain exactly one object", line), false)
	}
	return nil
}
