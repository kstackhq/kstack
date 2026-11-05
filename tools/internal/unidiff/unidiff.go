// Package unidiff reads the added lines of a unified diff, as `git diff`
// writes it.
package unidiff

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Added returns the lines each file of the diff adds, in order, keyed by the
// file's path in the new tree.
func Added(r io.Reader) (map[string][]int, error) {
	added := map[string][]int{}
	file := "" // the file the hunks belong to; empty for a deleted one
	// A hunk's body is read by its header's counts, so an added line that
	// reads "++ x" is not taken for a file header.
	var h hunk
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if h.oldLeft > 0 || h.newLeft > 0 {
			switch {
			case strings.HasPrefix(line, "+"):
				if file != "" {
					added[file] = append(added[file], h.next)
				}
				h.next++
				h.newLeft--
			case strings.HasPrefix(line, "-"):
				h.oldLeft--
			case strings.HasPrefix(line, " "):
				h.next++
				h.newLeft--
				h.oldLeft--
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "+++ "):
			var err error
			if file, err = newPath(strings.TrimPrefix(line, "+++ ")); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "@@ "):
			var err error
			if h, err = parseHunk(line); err != nil {
				return nil, err
			}
		}
	}
	return added, sc.Err()
}

// newPath reads the path of a `+++` line, empty for /dev/null. Git ends a
// path holding a space with a tab, and C-quotes one holding a tab, quote,
// backslash, control or non-ASCII byte.
func newPath(s string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		// Git's C escapes are a subset of Go's.
		unquoted, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("%q: %w", s, err)
		}
		s = unquoted
	}
	if s == "/dev/null" {
		return "", nil
	}
	return strings.TrimPrefix(s, "b/"), nil
}

// A hunk's position: the next new-side line, and the lines left on each side.
type hunk struct {
	next             int
	oldLeft, newLeft int
}

// parseHunk reads a hunk header, `@@ -a,b +c,d @@`. A count the header leaves
// out is 1.
func parseHunk(header string) (hunk, error) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return hunk{}, fmt.Errorf("%q: not a hunk header", header)
	}
	_, oldCount, err := span(fields[1][1:])
	if err != nil {
		return hunk{}, fmt.Errorf("%q: %w", header, err)
	}
	from, newCount, err := span(fields[2][1:])
	if err != nil {
		return hunk{}, fmt.Errorf("%q: %w", header, err)
	}
	return hunk{next: from, oldLeft: oldCount, newLeft: newCount}, nil
}

// span reads one side of a hunk header, `c,d` or `c`.
func span(s string) (int, int, error) {
	first, count, found := strings.Cut(s, ",")
	from, err := strconv.Atoi(first)
	if err != nil {
		return 0, 0, err
	}
	n := 1
	if found {
		if n, err = strconv.Atoi(count); err != nil {
			return 0, 0, err
		}
	}
	return from, n, nil
}
