// Command coverignore merges Go cover profiles into one and drops every block
// a marker exempts, so Codecov counts none of its lines. CI runs it on each
// OS's profiles before the upload.
//
//	go run ./coverignore -dir ../sidecar -o merged.out untagged.out debug.out
//
// -dir is the module the profiles measured: its go.mod maps a profile's import
// paths back to files on disk.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/cover"
)

// The markers. `// coverage:ignore <reason>` at the end of a line exempts the
// statement, or function, that begins on it, body included;
// `// coverage:ignore-start <reason>` through `// coverage:ignore-stop` exempt
// the lines between them. A marker without a reason exempts nothing;
// scripts/check-coverage-markers.sh fails CI on it.
var (
	inline = regexp.MustCompile(`^//\s*coverage:ignore\s+\S`)
	start  = regexp.MustCompile(`^//\s*coverage:ignore-start\s+\S`)
	stop   = regexp.MustCompile(`^//\s*coverage:ignore-stop\b`)
)

func main() {
	dir := flag.String("dir", ".", "the module the profiles measured")
	out := flag.String("o", "", "write the merged profile to `path`")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: coverignore -dir <module> -o <out> <profile>...")
		os.Exit(2)
	}
	if err := run(*dir, *out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "coverignore:", err)
		os.Exit(1)
	}
}

func run(dir, out string, inputs []string) error {
	module, err := modulePath(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	var mode string
	var all [][]*cover.Profile
	for _, in := range inputs {
		profiles, err := cover.ParseProfiles(in)
		if err != nil {
			return err
		}
		if len(profiles) > 0 {
			mode = profiles[0].Mode
		}
		all = append(all, profiles)
	}
	merged := merge(all)
	err = dropMarked(merged, func(name string) (string, error) {
		rel, ok := strings.CutPrefix(name, module+"/")
		if !ok {
			return "", fmt.Errorf("%s is outside module %s", name, module)
		}
		return filepath.Join(dir, filepath.FromSlash(rel)), nil
	})
	if err != nil {
		return err
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := write(f, mode, merged); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func modulePath(gomod string) (string, error) {
	data, err := os.ReadFile(gomod)
	if err != nil {
		return "", err
	}
	path := modfile.ModulePath(data)
	if path == "" {
		return "", fmt.Errorf("%s: no module line", gomod)
	}
	return path, nil
}

type blockKey struct {
	startLine, startCol, endLine, endCol int
}

// merge folds every profile of every input into one profile per file. The
// same block arrives once per test binary under -coverpkg, and once per input,
// so counts add up.
func merge(inputs [][]*cover.Profile) []*cover.Profile {
	files := map[string]map[blockKey]cover.ProfileBlock{}
	for _, profiles := range inputs {
		for _, p := range profiles {
			blocks := files[p.FileName]
			if blocks == nil {
				blocks = map[blockKey]cover.ProfileBlock{}
				files[p.FileName] = blocks
			}
			for _, b := range p.Blocks {
				k := blockKey{b.StartLine, b.StartCol, b.EndLine, b.EndCol}
				if have, ok := blocks[k]; ok {
					b.Count += have.Count
				}
				blocks[k] = b
			}
		}
	}

	merged := make([]*cover.Profile, 0, len(files))
	for name, blocks := range files {
		p := &cover.Profile{FileName: name}
		for _, b := range blocks {
			p.Blocks = append(p.Blocks, b)
		}
		sort.Slice(p.Blocks, func(i, j int) bool {
			a, b := p.Blocks[i], p.Blocks[j]
			if a.StartLine != b.StartLine {
				return a.StartLine < b.StartLine
			}
			return a.StartCol < b.StartCol
		})
		merged = append(merged, p)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].FileName < merged[j].FileName })
	return merged
}

// dropMarked removes, in place, every block that starts on a line the file's
// markers exempt. source maps a profile's file name to the file on disk.
func dropMarked(profiles []*cover.Profile, source func(name string) (string, error)) error {
	for _, p := range profiles {
		path, err := source(p.FileName)
		if err != nil {
			return err
		}
		exempt, err := exemptLines(path)
		if err != nil {
			return err
		}
		if len(exempt) == 0 {
			continue
		}
		kept := p.Blocks[:0]
		for _, b := range p.Blocks {
			if !exempt[b.StartLine] {
				kept = append(kept, b)
			}
		}
		p.Blocks = kept
	}
	return nil
}

// exemptLines is the set of lines the file's markers exempt. An inline marker
// takes the whole statement that begins on its line, so the body's block goes
// whether the cover tool starts it on the `if` or on the line below.
func exemptLines(path string) (map[int]bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	lineOf := func(pos token.Pos) int { return fset.Position(pos).Line }
	exempt := map[int]bool{}
	marked := map[int]bool{}
	from := 0
	for _, group := range file.Comments {
		for _, c := range group.List {
			switch {
			case inline.MatchString(c.Text):
				marked[lineOf(c.Pos())] = true
			case start.MatchString(c.Text):
				from = lineOf(c.Pos())
			case stop.MatchString(c.Text) && from > 0:
				for line := from; line <= lineOf(c.Pos()); line++ {
					exempt[line] = true
				}
				from = 0
			}
		}
	}
	if from > 0 {
		for line := from; line <= lineOf(file.End()); line++ {
			exempt[line] = true
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n.(type) {
		case ast.Stmt, *ast.FuncDecl:
		default:
			return true
		}
		first := lineOf(n.Pos())
		if !marked[first] {
			return true
		}
		for line := first; line <= lineOf(n.End()); line++ {
			exempt[line] = true
		}
		return false
	})
	return exempt, nil
}

func write(w io.Writer, mode string, profiles []*cover.Profile) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "mode: %s\n", mode)
	for _, p := range profiles {
		for _, b := range p.Blocks {
			fmt.Fprintf(bw, "%s:%d.%d,%d.%d %d %d\n", p.FileName, b.StartLine, b.StartCol, b.EndLine, b.EndCol, b.NumStmt, b.Count)
		}
	}
	return bw.Flush()
}
