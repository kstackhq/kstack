package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// notes is notes.json, the prose the skill writes. Each item cites the pull
// requests it describes; render adds their links.
type notes struct {
	Tag          string     `json:"tag"`
	Highlights   string     `json:"highlights"`
	New          []item     `json:"new"`
	Fixes        []item     `json:"fixes"`
	Performance  []item     `json:"performance"`
	Security     []item     `json:"security"`
	UpgradeNotes []item     `json:"upgrade_notes"`
	Omitted      []omission `json:"omitted"`
}

type item struct {
	Text string `json:"text"`
	PRs  []int  `json:"prs"`
}

// An omission is a feat, fix or ! pull request the notes leave out on purpose.
type omission struct {
	PR     int    `json:"pr"`
	Reason string `json:"reason"`
}

type section struct {
	key, title string
	items      []item
}

// sections lists the notes' sections in the order render writes them.
func (n notes) sections() []section {
	return []section{
		{"new", "New", n.New},
		{"fixes", "Fixes", n.Fixes},
		{"performance", "Performance", n.Performance},
		{"security", "Security", n.Security},
		{"upgrade_notes", "Upgrade notes", n.UpgradeNotes},
	}
}

func readNotes(path string) (notes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return notes{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var n notes
	if err := dec.Decode(&n); err != nil {
		return notes{}, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}
