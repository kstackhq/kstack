package desktop

import (
	"fmt"
	"io"
)

// progress returns a Download callback that redraws one line on out. It
// writes only when the line changes: Download calls it on every read.
func progress(out io.Writer) func(done, total int64) {
	var last string
	return func(done, total int64) {
		line := size(done)
		if total > 0 {
			line = fmt.Sprintf("%3d%% of %s", done*100/total, size(total))
		}
		if line != last {
			// Clear to the end of the line: a shorter redraw must not leave
			// the tail of the longer one before it.
			fmt.Fprintf(out, "\r%s\x1b[K", line)
			last = line
		}
	}
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
