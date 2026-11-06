package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// render writes the release's Markdown: the notes' prose with a link per
// citation, then every change in the range, the new contributors, the
// compare link and the checksums. Only the prose comes from notes.json; check
// has already kept links out of it.
func render(n notes, c collection) string {
	base := "https://github.com/" + c.Repo
	prLink := func(pr int) string { return fmt.Sprintf("[#%d](%s/pull/%d)", pr, base, pr) }

	var b strings.Builder
	b.WriteString(strings.TrimSpace(n.Highlights) + "\n")
	for _, s := range n.sections() {
		if len(s.items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", s.title)
		for _, it := range s.items {
			links := make([]string, len(it.PRs))
			for i, pr := range it.PRs {
				links[i] = prLink(pr)
			}
			fmt.Fprintf(&b, "- %s (%s)\n", strings.TrimSpace(it.Text), strings.Join(links, ", "))
		}
	}

	b.WriteString("\n<details>\n<summary>All changes</summary>\n\n")
	if len(c.Changes) == 0 {
		b.WriteString("No change touches this product.\n")
	}
	for _, ch := range c.Changes {
		if ch.PR != 0 {
			fmt.Fprintf(&b, "- %s (%s) by @%s\n", escape(ch.Subject), prLink(ch.PR), login(ch.Author))
		} else {
			fmt.Fprintf(&b, "- %s ([`%s`](%s/commit/%s))\n", escape(ch.Subject), ch.Commit[:min(7, len(ch.Commit))], base, ch.Commit)
		}
	}
	b.WriteString("\n</details>\n")

	if len(c.NewContributors) > 0 {
		b.WriteString("\n## New contributors\n\n")
		for _, nc := range c.NewContributors {
			fmt.Fprintf(&b, "- @%s made their first contribution in %s\n", login(nc.Login), prLink(nc.PR))
		}
	}

	if c.Previous != "" {
		fmt.Fprintf(&b, "\n**Full changelog**: [%s...%s](%s/compare/%s...%s)\n", c.Previous, c.Tag, base, c.Previous, c.Tag)
	} else {
		fmt.Fprintf(&b, "\n**Full changelog**: [%s](%s/commits/%s)\n", c.Tag, base, c.Tag)
	}
	b.WriteString("\nEach download's SHA-256 is in `SHA256SUMS`, signed in `SHA256SUMS.asc`.\n")
	return b.String()
}

// login drops the app/ prefix gh gives a GitHub App's login.
func login(s string) string {
	return strings.TrimPrefix(s, "app/")
}

var markdown = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`)

// escape keeps a commit subject from being read as Markdown, leaving its
// code spans as they are. The text after an unpaired backtick is no code
// span, so it is escaped too.
func escape(s string) string {
	parts := strings.Split(s, "`")
	for i := range parts {
		if i%2 == 0 || i == len(parts)-1 {
			parts[i] = markdown.Replace(parts[i])
		}
	}
	return strings.Join(parts, "`")
}

func (t tool) renderCmd(ctx context.Context, path string, out io.Writer) error {
	n, c, err := t.checked(ctx, path)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, render(n, c))
	return err
}
