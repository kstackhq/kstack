package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadDiffListsEachFilesAddedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "changes.diff")
	writeFile(t, path, `diff --git a/sidecar/a.go b/sidecar/a.go
index 1111111..2222222 100644
--- a/sidecar/a.go
+++ b/sidecar/a.go
@@ -3,0 +4,2 @@ func f() {
+	x := 1
+	_ = x
@@ -10 +12 @@ func g() {
-	return 1
+	return 2
@@ -20,3 +23,0 @@ func h() {
-	a
-	b
-	c
diff --git a/src/gone.ts b/src/gone.ts
deleted file mode 100644
--- a/src/gone.ts
+++ /dev/null
@@ -1,2 +0,0 @@
-a
-b
diff --git a/src/new.ts b/src/new.ts
new file mode 100644
--- /dev/null
+++ b/src/new.ts
@@ -0,0 +1,2 @@
+export const a = 1;
+export const b = 2;
`)
	got, err := readDiff(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"sidecar/a.go": {4, 5, 12},
		"src/new.ts":   {1, 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readDiff = %v, want %v", got, want)
	}
}

func TestReadDiffRefusesABadHunkHeader(t *testing.T) {
	for _, header := range []string{"@@ -1 @@", "@@ -1 +x @@", "@@ -1 +1,y @@"} {
		path := filepath.Join(t.TempDir(), "changes.diff")
		writeFile(t, path, "+++ b/a.go\n"+header+"\n")
		if _, err := readDiff(path); err == nil {
			t.Errorf("%q: readDiff succeeded", header)
		}
	}
}

func TestReadDiffNeedsTheFile(t *testing.T) {
	if _, err := readDiff(filepath.Join(t.TempDir(), "missing.diff")); err == nil {
		t.Error("readDiff succeeded")
	}
}
