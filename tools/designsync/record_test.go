package main

import (
	"io"
	"strings"
	"testing"
)

func TestRecordSetsTheWorkingTreesFingerprints(t *testing.T) {
	tl := fixtureTool(t)
	changed := pillSource + "// rounder\n"
	writeFile(t, tl, "src/status-pill.tsx", changed)
	louder := bellSource + "// louder\n"
	writeFile(t, tl, "src/bell-button.tsx", louder)

	if err := tl.recordCmd("", []string{"dashboard/status-pill", "shell/bell-button"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(blobHash([]byte(pillSource)), blobHash([]byte(changed)), blobHash([]byte(bellSource)), blobHash([]byte(louder))).Replace(testManifest)
	if got := readFile(t, tl, manifestPath); got != want {
		t.Errorf("manifest after record:\n%s", got)
	}
}

func TestRecordRefusesWhatItCannotFingerprint(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		setup    func(t *testing.T, tl tool)
		err      string
	}{
		{"unknown id", "chat/nope", nil, "chat/nope is not in design/components.json"},
		{"no code", "chat/badge", nil, "chat/badge: no code draws it yet"},
		{"missing source", "dashboard/status-pill", func(t *testing.T, tl tool) {
			editManifest(t, tl, "src/status-pill.tsx", "src/gone.tsx")
		}, "dashboard/status-pill: open"},
		{"no manifest", "shell/bell-button", func(t *testing.T, tl tool) {
			writeFile(t, tl, manifestPath, "{")
		}, "design/components.json:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tl := fixtureTool(t)
			if tc.setup != nil {
				tc.setup(t, tl)
			}
			before := readFile(t, tl, manifestPath)
			wantErr(t, tl.recordCmd("", []string{tc.id}, io.Discard), tc.err)
			if got := readFile(t, tl, manifestPath); got != before {
				t.Error("a failed record changed the manifest")
			}
		})
	}
}

func TestRecordSetsTheDesignFromTheStamps(t *testing.T) {
	tl := fixtureTool(t)
	stamps := t.TempDir() + "/stamps.json"
	writeStamps(t, stamps, `[
		{"node": "9:200", "name": "Pill", "hash": "00000000000000bb", "stamp": {"kstack": 1, "id": "dashboard/status-pill"}},
		{"node": "20:1", "name": "Badge", "hash": "00000000000000cc", "stamp": {"kstack": 1, "id": "chat/badge"}},
		{"node": "5:21", "name": "BellButton", "stamp": {"kstack": 1, "id": "shell/bell-button"}},
		{"node": "7:1", "name": "New", "hash": "00000000000000dd", "stamp": null}
	]`)

	if err := tl.recordCmd(stamps, []string{"dashboard/status-pill", "chat/badge"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(
		`"node": "9:148",
        "name": "StatusPill",
        "hash": "00000000000000aa"`, `"node": "9:200",
        "name": "Pill",
        "hash": "00000000000000bb"`,
		`"name": "Badge"`, `"name": "Badge",
        "hash": "00000000000000cc"`,
	).Replace(testManifest)
	if got := readFile(t, tl, manifestPath); got != want {
		t.Errorf("manifest after record:\n%s", got)
	}

	// A component the stamps file gives no hash is not recorded.
	wantErr(t, tl.recordCmd(stamps, []string{"shell/bell-button"}, io.Discard), "shell/bell-button: the stamps file has no hashed component")
	wantErr(t, tl.recordCmd(t.TempDir()+"/none.json", []string{"shell/bell-button"}, io.Discard), "none.json")
}

func TestRecordSetsAScreensFingerprints(t *testing.T) {
	tl := fixtureTool(t)
	withScreen(t, tl)
	before := readFile(t, tl, manifestPath)
	changed := screenSource + "// wider\n"
	writeFile(t, tl, "src/dashboard.tsx", changed)
	stamps := t.TempDir() + "/stamps.json"
	writeStampsWithScreens(t, stamps, "[]", "["+strings.Replace(screenStamp, "00000000000000ee", "00000000000000ef", 1)+"]")

	if err := tl.recordCmd(stamps, []string{"screens/dashboard"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(blobHash([]byte(screenSource)), blobHash([]byte(changed)), "00000000000000ee", "00000000000000ef").Replace(before)
	if got := readFile(t, tl, manifestPath); got != want {
		t.Errorf("manifest after record:\n%s", got)
	}
}
