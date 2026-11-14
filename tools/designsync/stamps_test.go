package main

import (
	"os"
	"strings"
	"testing"
)

// The sums are what design/scripts/read-stamps.js's hash returns in Node.
func TestJSHashIsTheScripts(t *testing.T) {
	for s, want := range map[string]string{
		"":                    "cbf29ce484222325",
		"9:148\nStatusPill":   "10b9e6a5265555dc",
		"é → \U0001F600 done": "60c3b69422f49bdd",
	} {
		if got := jsHash(s); got != want {
			t.Errorf("jsHash(%q) = %s, want %s", s, got, want)
		}
	}
}

func TestChecksumCoversEveryFieldTheToolReads(t *testing.T) {
	base := stamped{Node: "9:148", Name: "StatusPill", Hash: "00000000000000aa", Stamp: &stamp{Kstack: 1, ID: "dashboard/status-pill", Changes: []string{"Rounder."}}}
	sum := checksum([]stamped{base})

	edits := map[string]func(*stamped){
		"node":    func(s *stamped) { s.Node = "9:149" },
		"name":    func(s *stamped) { s.Name = "Pill" },
		"hash":    func(s *stamped) { s.Hash = "00000000000000ab" },
		"id":      func(s *stamped) { s.Stamp = &stamp{ID: "dashboard/pill", Changes: base.Stamp.Changes} },
		"changes": func(s *stamped) { s.Stamp = &stamp{ID: base.Stamp.ID} },
		"stamp":   func(s *stamped) { s.Stamp = nil },
	}
	for name, edit := range edits {
		s := base
		edit(&s)
		if checksum([]stamped{s}) == sum {
			t.Errorf("a changed %s leaves the checksum as it was", name)
		}
	}
	if checksum([]stamped{base, base}) == sum {
		t.Error("a repeated component leaves the checksum as it was")
	}
}

func TestLoadStampsRefusesAFileThatWasRetyped(t *testing.T) {
	path := t.TempDir() + "/stamps.json"
	writeStamps(t, path, "["+pillStamp+"]")
	if got, err := loadStamps(path); err != nil || len(got) != 1 {
		t.Fatalf("loadStamps = %v, %v", got, err)
	}

	// One digit of the hash, as an agent retyping the file gets it wrong.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	retyped := strings.Replace(string(data), "00000000000000aa", "00000000000000ab", 1)
	if err := os.WriteFile(path, []byte(retyped), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = loadStamps(path)
	wantErr(t, err, "save what the script returns unchanged")
}

func TestLoadStampsReadsTheScreensUnderTheSameChecksum(t *testing.T) {
	path := t.TempDir() + "/stamps.json"
	writeStampsWithScreens(t, path, "["+pillStamp+"]", "["+screenStamp+"]")
	got, err := loadStamps(path)
	if err != nil || len(got) != 2 || got[1].Stamp.ID != "screens/dashboard" {
		t.Fatalf("loadStamps = %v, %v", got, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	retyped := strings.Replace(string(data), "00000000000000ee", "00000000000000ef", 1)
	if err := os.WriteFile(path, []byte(retyped), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = loadStamps(path)
	wantErr(t, err, "save what the script returns unchanged")
}

func TestLoadStampsReadsNothingWithoutAPath(t *testing.T) {
	if got, err := loadStamps(""); got != nil || err != nil {
		t.Errorf("loadStamps(\"\") = %v, %v", got, err)
	}
}
