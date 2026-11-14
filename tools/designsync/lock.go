package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v4"
)

const lockPath = "pnpm-lock.yaml"

// lockVersions returns the version pnpm-lock.yaml resolves each of the
// webview's dependencies to. The file holds two YAML documents, pnpm's own
// dependencies and then the project's.
func (t tool) lockVersions() (map[string]string, error) {
	data, err := t.read(lockPath)
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var lock struct {
			Importers map[string]struct {
				Dependencies map[string]struct {
					Version string `yaml:"version"`
				} `yaml:"dependencies"`
			} `yaml:"importers"`
		}
		if err := dec.Decode(&lock); errors.Is(err, io.EOF) {
			return versions, nil
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", lockPath, err)
		}
		for name, dep := range lock.Importers["."].Dependencies {
			// The peers a package resolved against follow its version in brackets.
			versions[name], _, _ = strings.Cut(dep.Version, "(")
		}
	}
}
