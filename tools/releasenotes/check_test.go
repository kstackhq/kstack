package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A collection like cli/v0.2.0's: #5 fix and #3 breaking must be cited, #4
// docs need not be, and one commit has no pull request.
func testCollection() collection {
	return collection{
		Repo:     testRepo,
		Tag:      "cli/v0.2.0",
		Previous: "cli/v0.1.0",
		Range:    "cli/v0.1.0..cli/v0.2.0",
		Changes: []change{
			{Commit: "5555555555", Subject: "fix(cli): handle empty", PR: 5, MustCite: true, Author: "app/dependabot"},
			{Commit: "0000000000", Subject: "tweak the cli by hand"},
			{Commit: "4444444444", Subject: "docs(cli): tweak the help", PR: 4, Author: "bob"},
			{Commit: "3333333333", Subject: "feat(cli)!: rename the flag", PR: 3, MustCite: true, Breaking: true, Author: "alice"},
		},
		NewContributors: []contributor{{"bob", 4}},
	}
}

// testNotes passes against testCollection and fixtureRepo's cli/v0.2.0.
func testNotes() notes {
	return notes{
		Tag:          "cli/v0.2.0",
		Highlights:   "The flag has a new name.",
		New:          []item{{Text: "The flag is now `--name`.", PRs: []int{3}}},
		Fixes:        []item{{Text: "An empty input no longer fails.", PRs: []int{5}}},
		UpgradeNotes: []item{{Text: "Rename `--flag` to `--name` in scripts.", PRs: []int{3}}},
	}
}

func TestCheckPassesGoodNotes(t *testing.T) {
	if problems := check(testNotes(), testCollection()); len(problems) != 0 {
		t.Errorf("problems = %q", problems)
	}
}

func TestCheckFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*notes)
		want   string
	}{
		{"a citation outside the range", func(n *notes) { n.Fixes[0].PRs = append(n.Fixes[0].PRs, 2) },
			"fixes[0] cites #2, which is not in cli/v0.1.0..cli/v0.2.0"},
		{"a dropped fix", func(n *notes) { n.Fixes = nil },
			"#5 (fix(cli): handle empty) is neither cited nor omitted with a reason"},
		{"a dropped breaking change", func(n *notes) { n.New, n.UpgradeNotes = nil, nil },
			"#3 (feat(cli)!: rename the flag) is neither cited nor omitted"},
		{"an omission without a reason", func(n *notes) { n.Fixes, n.Omitted = nil, []omission{{PR: 5, Reason: " "}} },
			"omitted[0] gives no reason for #5"},
		{"an omission outside the range", func(n *notes) { n.Omitted = []omission{{PR: 9, Reason: "x"}} },
			"omitted[0] names #9, which is not in"},
		{"an omission an item cites", func(n *notes) { n.Omitted = []omission{{PR: 5, Reason: "x"}} },
			"omitted[0] names #5, which an item cites"},
		{"an item citing nothing", func(n *notes) { n.Security = []item{{Text: "Safer."}} },
			"security[0] cites no pull request"},
		{"empty highlights", func(n *notes) { n.Highlights = " " }, "highlights is empty"},
		{"a link", func(n *notes) { n.New[0].Text = "See [the docs](https://example.com)." }, `new[0] contains "://"`},
		{"a bare www link", func(n *notes) { n.New[0].Text = "See www.example.com." }, `contains "www."`},
		{"html", func(n *notes) { n.Highlights = "<img src=x>" }, `highlights contains "<"`},
		{"a mention", func(n *notes) { n.New[0].Text = "Thanks @bob." }, `contains "@"`},
		{"a #N", func(n *notes) { n.New[0].Text = "As in #3." }, "names a pull request as #N"},
		{"two lines", func(n *notes) { n.Highlights = "One.\n\nTwo." }, "spans lines"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := testNotes()
			tc.change(&n)
			problems := check(n, testCollection())
			if !strings.Contains(strings.Join(problems, "\n"), tc.want) {
				t.Errorf("problems = %q, want %q", problems, tc.want)
			}
		})
	}
}

func TestCheckAcceptsAnOmissionWithAReason(t *testing.T) {
	n := testNotes()
	n.Fixes = nil
	n.Omitted = []omission{{PR: 5, Reason: "a bump no user sees"}}
	if problems := check(n, testCollection()); len(problems) != 0 {
		t.Errorf("problems = %q", problems)
	}
}

func TestTextInCodeSpansIsLeftAlone(t *testing.T) {
	if p := textProblem("Bump `@types/node` and `<T>` past `#3`."); p != "" {
		t.Errorf("textProblem = %q", p)
	}
}

func writeNotes(t *testing.T, n notes) string {
	t.Helper()
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "notes.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckCmd(t *testing.T) {
	ctx := context.Background()
	tl, _ := fixtureTool(t)
	var out strings.Builder
	if err := tl.checkCmd(ctx, writeNotes(t, testNotes()), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "passes against cli/v0.1.0..cli/v0.2.0") {
		t.Errorf("out = %q", out.String())
	}

	bad := testNotes()
	bad.Fixes[0].PRs = []int{2}
	err := tl.checkCmd(ctx, writeNotes(t, bad), &out)
	if err == nil || !strings.Contains(err.Error(), "fails the check:\n  - fixes[0] cites #2") {
		t.Errorf("err = %v", err)
	}

	bad = testNotes()
	bad.Tag = "cli/v9.0.0"
	if err := tl.checkCmd(ctx, writeNotes(t, bad), &out); err == nil {
		t.Error("checked notes for a missing tag")
	}
	if err := tl.checkCmd(ctx, filepath.Join(t.TempDir(), "none.json"), &out); err == nil {
		t.Error("checked a missing file")
	}
}
