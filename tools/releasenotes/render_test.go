package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	got := render(testNotes(), testCollection())
	want := "The flag has a new name.\n" +
		"\n## New\n\n" +
		"- The flag is now `--name`. ([#3](https://github.com/o/r/pull/3))\n" +
		"\n## Fixes\n\n" +
		"- An empty input no longer fails. ([#5](https://github.com/o/r/pull/5))\n" +
		"\n## Upgrade notes\n\n" +
		"- Rename `--flag` to `--name` in scripts. ([#3](https://github.com/o/r/pull/3))\n" +
		"\n<details>\n<summary>All changes</summary>\n\n" +
		"- fix(cli): handle empty ([#5](https://github.com/o/r/pull/5)) by @dependabot\n" +
		"- tweak the cli by hand ([`0000000`](https://github.com/o/r/commit/0000000000))\n" +
		"- docs(cli): tweak the help ([#4](https://github.com/o/r/pull/4)) by @bob\n" +
		"- feat(cli)!: rename the flag ([#3](https://github.com/o/r/pull/3)) by @alice\n" +
		"\n</details>\n" +
		"\n## New contributors\n\n" +
		"- @bob made their first contribution in [#4](https://github.com/o/r/pull/4)\n" +
		"\n**Full changelog**: [cli/v0.1.0...cli/v0.2.0](https://github.com/o/r/compare/cli/v0.1.0...cli/v0.2.0)\n" +
		"\nEach download's SHA-256 is in `SHA256SUMS`, signed in `SHA256SUMS.asc`.\n"
	if got != want {
		t.Errorf("render =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderAFirstReleaseWithNothingInIt(t *testing.T) {
	c := collection{Repo: testRepo, Tag: "cli/v0.1.0", Range: "cli/v0.1.0"}
	got := render(notes{Highlights: "First."}, c)
	for _, want := range []string{
		"No change touches this product.",
		"**Full changelog**: [cli/v0.1.0](https://github.com/o/r/commits/cli/v0.1.0)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"## New", "## New contributors"} {
		if strings.Contains(got, unwanted+"\n") {
			t.Errorf("render has %q:\n%s", unwanted, got)
		}
	}
}

func TestEscape(t *testing.T) {
	for in, want := range map[string]string{
		"fix: a [link](x) <b>*bold*</b>": `fix: a \[link\](x) \<b\>\*bold\*\</b\>`,
		"feat: add `--a_b` and a_b":      "feat: add `--a_b` and a\\_b",
		"fix: one ` tick [a](b)":         "fix: one ` tick \\[a\\](b)",
		`a \ backslash ~strike~`:         `a \\ backslash \~strike\~`,
	} {
		if got := escape(in); got != want {
			t.Errorf("escape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderCmd(t *testing.T) {
	ctx := context.Background()
	tl, _ := fixtureTool(t)
	var out strings.Builder
	if err := tl.renderCmd(ctx, writeNotes(t, testNotes()), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- An empty input no longer fails. ([#5](https://github.com/o/r/pull/5))",
		"- tweak the cli by hand ([`",
		"- @bob made their first contribution in [#4]",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("render lacks %q:\n%s", want, out.String())
		}
	}

	// Notes that fail the check are not rendered.
	bad := testNotes()
	bad.Fixes = nil
	out.Reset()
	if err := tl.renderCmd(ctx, writeNotes(t, bad), &out); err == nil || out.Len() != 0 {
		t.Errorf("rendered failing notes: %v %q", err, out.String())
	}
	if err := tl.renderCmd(ctx, filepath.Join(t.TempDir(), "none.json"), &out); err == nil {
		t.Error("rendered a missing file")
	}
}
