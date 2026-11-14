package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// The stamps file is what design/scripts/read-stamps.js returns, saved by the
// agent that ran it.
type stampsFile struct {
	Components []stamped `json:"components"`
	Screens    []stamped `json:"screens"`
	Sum        string    `json:"sum"` // checksum's value when the script returned
}

// A stamped is one Figma component or screen.
type stamped struct {
	Node    string `json:"node"`
	Name    string `json:"name"`
	Hash    string `json:"hash"` // of what it looks like
	Stamp   *stamp `json:"stamp"`
	Invalid bool   `json:"invalid"` // a stamp is there and does not parse
}

// A stamp is the JSON block a Figma component's description ends with, or a
// screen's Stamp layer holds.
type stamp struct {
	Kstack  int      `json:"kstack"`
	ID      string   `json:"id"`
	Changes []string `json:"changes"`
}

// loadStamps reads the stamps file's components and screens, or nothing when
// there is no path. It
// refuses a file whose checksum is off: the file reached the disk through an
// agent, which can mistype it.
func loadStamps(path string) ([]stamped, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f stampsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	all := append(f.Components, f.Screens...)
	if got := checksum(all); got != f.Sum {
		return nil, fmt.Errorf("%s: its checksum is %s, not the %s read-stamps.js gave it: save what the script returns unchanged", path, got, f.Sum)
	}
	return all, nil
}

// checksum is read-stamps.js's sum over the same fields, in the same order.
func checksum(components []stamped) string {
	var fields []string
	for _, c := range components {
		fields = append(fields, c.Node, c.Name, c.Hash)
		if c.Stamp == nil {
			fields = append(fields, "")
			continue
		}
		fields = append(fields, c.Stamp.ID)
		fields = append(fields, c.Stamp.Changes...)
	}
	return jsHash(strings.Join(fields, "\n"))
}

// jsHash is read-stamps.js's hash: 64-bit FNV-1a over UTF-16 code units, which
// is how JavaScript indexes a string.
func jsHash(s string) string {
	h := uint64(0xcbf29ce484222325)
	for _, unit := range utf16.Encode([]rune(s)) {
		h ^= uint64(unit)
		h *= 0x100000001b3
	}
	return fmt.Sprintf("%016x", h)
}
