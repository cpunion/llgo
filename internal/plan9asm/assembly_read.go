package plan9asm

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func readAssemblyFileBounded(overlay map[string][]byte, path string, limit int) ([]byte, error) {
	if err := validateAssemblyReadLimit(limit); err != nil {
		return nil, err
	}
	if source, exists := overlay[path]; exists {
		if len(source) > limit {
			return nil, fmt.Errorf("assembly source %s exceeds %d-byte inventory bound", path, limit)
		}
		return source, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return readAssemblyStreamBounded(file, limit)
}

func readAssemblyStreamBounded(source io.ReadCloser, limit int) (body []byte, err error) {
	defer func() {
		if closeErr := source.Close(); closeErr != nil {
			body = nil
			err = errors.Join(err, fmt.Errorf("close assembly source: %w", closeErr))
		}
	}()
	if err := validateAssemblyReadLimit(limit); err != nil {
		return nil, err
	}
	// One extra byte distinguishes exact-size input from an oversized source.
	// Enforce the bound before ReadAll allocates, including on non-regular files.
	body, err = io.ReadAll(io.LimitReader(source, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("assembly source exceeds %d-byte inventory bound", limit)
	}
	return body, err
}

func validateAssemblyReadLimit(limit int) error {
	if limit < 0 || int64(limit) == 1<<63-1 {
		return fmt.Errorf("invalid assembly read bound %d", limit)
	}
	return nil
}
