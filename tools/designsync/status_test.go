package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// statusOf runs status with the stamps, or with none when stamps is empty.
func statusOf(t *testing.T, tl tool, stamps string) string {
	t.Helper()
	var path string
	if stamps != "" {
		path = t.TempDir() + "/stamps.json"
		writeStamps(t, path, stamps)
	}
	var out strings.Builder
	if err := tl.statusCmd(path, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func wantLines(t *testing.T, got string, lines ...string) {
	t.Helper()
	for _, want := range lines {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
}

const (
	pillStamp  = `{"node": "9:148", "name": "StatusPill", "hash": "00000000000000aa", "stamp": {"kstack": 1, "id": "dashboard/status-pill"}}`
	bellStamp  = `{"node": "5:21", "name": "BellButton", "stamp": {"kstack": 1, "id": "shell/bell-button"}}`
	badgeStamp = `{"node": "20:1", "name": "Badge", "stamp": {"kstack": 1, "id": "chat/badge", "changes": ["A badge."]}}`
)

func TestStatusOfTheCodeSideAlone(t *testing.T) {
	tl := fixtureTool(t)
	got := statusOf(t, tl, "")
	wantLines(t, got, "not built  chat/badge", "1 not built, 2 in sync", "Code side only")
	if strings.Contains(got, "status-pill") {
		t.Errorf("status lists a component in sync:\n%s", got)
	}
}

func TestStatusNamesTheSideThatMoved(t *testing.T) {
	changes := `{"node": "9:148", "name": "StatusPill", "hash": "00000000000000bb", "stamp": {"kstack": 1, "id": "dashboard/status-pill", "changes": ["Rounder.", "Taller."]}}`

	t.Run("in sync", func(t *testing.T) {
		got := statusOf(t, fixtureTool(t), "["+pillStamp+","+bellStamp+","+badgeStamp+"]")
		if want := "not built  chat/badge  1 design change\n1 not built, 2 in sync\n"; got != want {
			t.Errorf("status = %q, want %q", got, want)
		}
	})
	t.Run("code ahead", func(t *testing.T) {
		tl := fixtureTool(t)
		writeFile(t, tl, "src/status-pill.tsx", pillSource+"// rounder\n")
		writeFile(t, tl, "src/bell-button.tsx", bellSource+"// louder\n")
		wantLines(t, statusOf(t, tl, ""),
			"code ahead  dashboard/status-pill  src/status-pill.tsx",
			"code ahead  shell/bell-button      src/bell-button.tsx",
			"2 code ahead, 1 not built")
	})
	t.Run("source gone", func(t *testing.T) {
		tl := fixtureTool(t)
		editManifest(t, tl, "src/status-pill.tsx", "src/gone.tsx")
		wantLines(t, statusOf(t, tl, ""), "code ahead  dashboard/status-pill  src/gone.tsx is gone")
	})
	t.Run("design ahead", func(t *testing.T) {
		wantLines(t, statusOf(t, fixtureTool(t), "["+changes+","+bellStamp+","+badgeStamp+"]"),
			"design ahead  dashboard/status-pill  2 design changes")
	})
	// The hash differs from the manifest's and the stamp declares nothing.
	edited := strings.Replace(pillStamp, "00000000000000aa", "00000000000000bb", 1)
	t.Run("edited by hand", func(t *testing.T) {
		wantLines(t, statusOf(t, fixtureTool(t), "["+edited+","+bellStamp+","+badgeStamp+"]"),
			"design ahead  dashboard/status-pill  edited in Figma, with no changes written on its stamp")
	})
	t.Run("edited by hand and code ahead", func(t *testing.T) {
		tl := fixtureTool(t)
		writeFile(t, tl, "src/status-pill.tsx", pillSource+"// rounder\n")
		wantLines(t, statusOf(t, tl, "["+edited+","+bellStamp+","+badgeStamp+"]"),
			"diverged   dashboard/status-pill  src/status-pill.tsx, edited in Figma")
	})
	t.Run("diverged", func(t *testing.T) {
		tl := fixtureTool(t)
		writeFile(t, tl, "src/status-pill.tsx", pillSource+"// rounder\n")
		wantLines(t, statusOf(t, tl, "["+changes+","+bellStamp+","+badgeStamp+"]"),
			"diverged   dashboard/status-pill  src/status-pill.tsx, 2 design changes")
	})
}

func TestStatusListsWhatTheStampsAndTheManifestDisagreeOn(t *testing.T) {
	got := statusOf(t, fixtureTool(t), `[
		{"node": "9:999", "name": "StatusPill", "stamp": {"kstack": 1, "id": "dashboard/status-pill"}},
		{"node": "5:21", "name": "AlarmButton", "stamp": {"kstack": 1, "id": "shell/bell-button"}},
		{"node": "5:22", "name": "CopyButton", "stamp": {"kstack": 1, "id": "shell/bell-button"}},
		{"node": "7:1", "name": "New", "stamp": null},
		{"node": "7:2", "name": "Broken", "stamp": null, "invalid": true},
		{"node": "7:3", "name": "Stray", "stamp": {"kstack": 1, "id": "chat/stray"}}
	]`)
	wantLines(t, got,
		"dashboard/status-pill  its stamp is on node 9:999, not 9:148",
		"shell/bell-button      renamed in Figma: BellButton is now AlarmButton",
		"CopyButton (5:22)      carries AlarmButton's stamp too: shell/bell-button",
		"New (7:1)              has no stamp",
		"Broken (7:2)           its stamp is not valid JSON",
		"Stray (7:3)            its stamp's id is not in the manifest: chat/stray",
		"chat/badge             nothing in Figma carries its stamp",
		"7 unmatched")
}

func TestStatusReportsWhatItCannotRead(t *testing.T) {
	tl := fixtureTool(t)
	wantErr(t, tl.statusCmd(t.TempDir()+"/none.json", io.Discard), "none.json")

	bad := t.TempDir() + "/bad.json"
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr(t, tl.statusCmd(bad, io.Discard), "bad.json:")

	writeFile(t, tl, manifestPath, "{")
	wantErr(t, tl.statusCmd("", io.Discard), "design/components.json:")
}

const screenStamp = `{"node": "42:1", "name": "Dashboard", "hash": "00000000000000ee", "stamp": {"kstack": 1, "id": "screens/dashboard"}}`

func TestStatusListsAScreenAsAComponent(t *testing.T) {
	components := "[" + pillStamp + "," + bellStamp + "," + badgeStamp + "]"
	statusWith := func(t *testing.T, tl tool, screen string) string {
		t.Helper()
		path := t.TempDir() + "/stamps.json"
		writeStampsWithScreens(t, path, components, "["+screen+"]")
		var out strings.Builder
		if err := tl.statusCmd(path, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	t.Run("in sync", func(t *testing.T) {
		tl := fixtureTool(t)
		withScreen(t, tl)
		wantLines(t, statusWith(t, tl, screenStamp), "1 not built, 3 in sync")
	})
	t.Run("code ahead", func(t *testing.T) {
		tl := fixtureTool(t)
		withScreen(t, tl)
		writeFile(t, tl, "src/dashboard.tsx", screenSource+"// wider\n")
		wantLines(t, statusOf(t, tl, ""), "code ahead  screens/dashboard  src/dashboard.tsx")
	})
	t.Run("edited by hand", func(t *testing.T) {
		tl := fixtureTool(t)
		withScreen(t, tl)
		edited := strings.Replace(screenStamp, "00000000000000ee", "00000000000000ef", 1)
		wantLines(t, statusWith(t, tl, edited), "design ahead  screens/dashboard  edited in Figma")
	})
	t.Run("no stamp", func(t *testing.T) {
		tl := fixtureTool(t)
		withScreen(t, tl)
		wantLines(t, statusWith(t, tl, `{"node": "42:1", "name": "Dashboard", "hash": "00000000000000ee", "stamp": null}`),
			"unmatched  Dashboard (42:1)   has no stamp",
			"unmatched  screens/dashboard  nothing in Figma carries its stamp")
	})
}
