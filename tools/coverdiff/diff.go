package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// readDiff returns the lines each file of the unified diff at path adds, in
// order, keyed by the file's path in the new tree.
func readDiff(path string) (map[string][]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	added := map[string][]int{}
	file := "" // the file the hunks belong to; empty for a deleted one
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			if file == "/dev/null" {
				file = ""
			}
		case strings.HasPrefix(line, "@@ ") && file != "":
			from, count, err := hunk(line)
			if err != nil {
				return nil, err
			}
			for n := from; n < from+count; n++ {
				added[file] = append(added[file], n)
			}
		}
	}
	return added, sc.Err()
}

// hunk reads the new side of a hunk header, `@@ -a,b +c,d @@`: the first line
// and the count, which is 1 when the header leaves it out.
func hunk(header string) (int, int, error) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, fmt.Errorf("%q: not a hunk header", header)
	}
	first, count, found := strings.Cut(fields[2][1:], ",")
	from, err := strconv.Atoi(first)
	if err != nil {
		return 0, 0, fmt.Errorf("%q: %w", header, err)
	}
	n := 1
	if found {
		if n, err = strconv.Atoi(count); err != nil {
			return 0, 0, fmt.Errorf("%q: %w", header, err)
		}
	}
	return from, n, nil
}
