package desktop

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProgressRedrawsOneLine(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		done, total int64
		want        string
	}{
		{0, 10, "\r  0% of 10 B\x1b[K"},
		{1536, 3072, "\r 50% of 3.0 KB\x1b[K"},
		{27 << 20, 27 << 20, "\r100% of 27.0 MB\x1b[K"},
		{5 << 20, -1, "\r5.0 MB\x1b[K"},
	} {
		var out bytes.Buffer
		progress(&out)(c.done, c.total)
		assert.Equal(t, c.want, out.String())
	}
}

func TestProgressSkipsAnUnchangedLine(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := progress(&out)
	p(1, 1000)
	p(2, 1000)
	p(10, 1000)
	assert.Equal(t, "\r  0% of 1000 B\x1b[K\r  1% of 1000 B\x1b[K", out.String())
}
