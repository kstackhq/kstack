package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const source = `package demo

func f(err error) error {
	if err != nil { // coverage:ignore only a full disk fails here
		return err
	}
	if err == nil { // coverage:ignore
		return nil
	}
	return nil
}

func g() int { // coverage:ignore only the sandbox's child runs this
	if true {
		return 1
	}
	return 2
}

func h(err error) int {
	if err != nil {
		return 0
	}
	// coverage:ignore-start only the sandbox's child runs this
	println()
	return 3
	// coverage:ignore-stop
}
`

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunMergesTheProfilesAndDropsMarkedBlocks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mod", "go.mod"), "module example.com/mod\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "mod", "demo", "demo.go"), source)
	// Two test binaries report the same blocks, and a second run adds a hit.
	// Line 4's body is reported as Go 1.26 starts it, on the if, and as 1.27
	// does, on the line below.
	writeFile(t, filepath.Join(dir, "a.out"), `mode: atomic
example.com/mod/demo/demo.go:3.28,4.16 1 1
example.com/mod/demo/demo.go:4.16,6.3 1 0
example.com/mod/demo/demo.go:5.3,5.13 1 0
example.com/mod/demo/demo.go:7.16,9.3 1 0
example.com/mod/demo/demo.go:10.2,10.12 1 0
example.com/mod/demo/demo.go:3.28,4.16 1 0
example.com/mod/demo/demo.go:13.14,14.10 1 0
example.com/mod/demo/demo.go:14.10,16.3 1 0
example.com/mod/demo/demo.go:17.2,17.10 1 0
example.com/mod/demo/demo.go:20.24,21.16 1 1
example.com/mod/demo/demo.go:21.16,23.3 1 0
example.com/mod/demo/demo.go:25.2,26.10 1 0
`)
	writeFile(t, filepath.Join(dir, "b.out"), `mode: atomic
example.com/mod/demo/demo.go:10.2,10.12 1 2
`)

	out := filepath.Join(dir, "merged.out")
	if err := run(filepath.Join(dir, "mod"), out, []string{filepath.Join(dir, "a.out"), filepath.Join(dir, "b.out")}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// Line 4's marker takes its if whole, however the body's block is
	// numbered; line 7's has no reason, so its block stays; g's marker on
	// the func line takes the function; h keeps what precedes its range.
	want := `mode: atomic
example.com/mod/demo/demo.go:3.28,4.16 1 1
example.com/mod/demo/demo.go:7.16,9.3 1 0
example.com/mod/demo/demo.go:10.2,10.12 1 2
example.com/mod/demo/demo.go:20.24,21.16 1 1
example.com/mod/demo/demo.go:21.16,23.3 1 0
`
	if string(got) != want {
		t.Errorf("merged profile:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunRefusesAFileOutsideTheModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/mod\n")
	writeFile(t, filepath.Join(dir, "p.out"), "mode: set\nexample.com/other/x.go:1.1,2.2 1 1\n")

	err := run(dir, filepath.Join(dir, "merged.out"), []string{filepath.Join(dir, "p.out")})
	if err == nil || !strings.Contains(err.Error(), "outside module") {
		t.Fatalf("err = %v, want one naming the module", err)
	}
}

func TestModulePathNeedsAModuleLine(t *testing.T) {
	gomod := filepath.Join(t.TempDir(), "go.mod")
	writeFile(t, gomod, "go 1.26\n")

	if _, err := modulePath(gomod); err == nil {
		t.Fatal("want an error for a go.mod with no module line")
	}
}
