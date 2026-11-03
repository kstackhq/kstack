package desktop

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Locate looks where each platform's installers put Kstack.
func (s *System) Locate() (string, error) {
	switch s.OS {
	case "darwin":
		return firstExisting(
			filepath.Join(s.Root, "Applications", "Kstack.app"),
			filepath.Join(s.Home, "Applications", "Kstack.app"),
		), nil
	case "windows":
		var candidates []string
		for _, env := range []string{"LOCALAPPDATA", "ProgramFiles"} {
			if dir := s.Getenv(env); dir != "" {
				candidates = append(candidates, filepath.Join(dir, "Kstack", "kstack.exe"))
			}
		}
		return firstExisting(candidates...), nil
	case "linux":
		return s.locateLinux(), nil
	}
	return "", s.unsupported()
}

// locateLinux tries the .deb and .rpm packages' binary, then a desktop
// entry's Exec, then an AppImage in the usual folders. It never searches
// PATH: another copy of this CLI there cannot be told from the app.
func (s *System) locateLinux() string {
	bin := filepath.Join(s.Root, "usr", "bin", "kstack")
	if isExecutable(bin) && !s.isSelf(bin) {
		return bin
	}
	for _, entry := range []string{
		filepath.Join(s.Home, ".local", "share", "applications", "Kstack.desktop"),
		filepath.Join(s.Root, "usr", "share", "applications", "Kstack.desktop"),
	} {
		if exe := desktopExec(entry); filepath.IsAbs(exe) && isExecutable(exe) {
			return exe
		}
	}
	return firstExisting(
		filepath.Join(s.Home, "Applications", "Kstack.AppImage"),
		filepath.Join(s.Home, ".local", "bin", "Kstack.AppImage"),
	)
}

func (s *System) isSelf(path string) bool {
	a, errA := os.Stat(path)
	b, errB := os.Stat(s.Self)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

// desktopExec returns the program a desktop entry's Exec line runs, or "".
func desktopExec(entry string) string {
	f, err := os.Open(entry)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if cmd, ok := strings.CutPrefix(sc.Text(), "Exec="); ok {
			if fields := strings.Fields(cmd); len(fields) > 0 {
				return fields[0]
			}
			return ""
		}
	}
	return ""
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// isExecutable reports whether path is a regular file with an execute bit.
// Windows has no such bit; only the Linux tests reach here on it.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode()&0o111 != 0
}
