package desktop

import (
	"os"
	"path/filepath"
	"runtime"
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
		// Unset, the path would be relative to the working directory.
		dir := s.Getenv("ProgramFiles")
		if dir == "" {
			return "", nil
		}
		return firstExisting(filepath.Join(dir, "Kstack", "kstack-desktop.exe")), nil
	case "linux":
		// The .deb's and the .rpm's binary.
		if bin := filepath.Join(s.Root, "usr", "bin", "kstack-desktop"); isExecutable(bin) {
			return bin, nil
		}
		return "", nil
	}
	return "", s.unsupported()
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
