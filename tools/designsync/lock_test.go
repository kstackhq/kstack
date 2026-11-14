package main

import "testing"

func TestLockVersionsReadsEveryDocument(t *testing.T) {
	tl := fixtureTool(t)
	got, err := tl.lockVersions()
	if err != nil {
		t.Fatal(err)
	}
	if got["lucide-react"] != "1.2.0" || got["pnpm"] != "12.0.0" {
		t.Errorf("versions = %v", got)
	}
}

func TestLockVersionsReportsABadLockfile(t *testing.T) {
	tl := fixtureTool(t)
	writeFile(t, tl, lockPath, "importers: [")
	_, err := tl.lockVersions()
	wantErr(t, err, "pnpm-lock.yaml:")

	_, err = tool{root: t.TempDir()}.lockVersions()
	wantErr(t, err, "pnpm-lock.yaml")
}
