package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Install installs the app from the installer Download returned, then removes
// the installer's directory. On macOS it installs for this user alone; the
// MSI asks Windows for admin rights, and the Linux package installs through
// sudo, the command printed to out first.
func (s *System) Install(ctx context.Context, installer string, out io.Writer) (string, error) {
	defer os.RemoveAll(filepath.Dir(installer))
	var err error
	switch s.OS {
	case "darwin":
		err = s.installDiskImage(ctx, installer)
	case "windows":
		// /passive shows progress and Windows' admin prompt, and asks nothing else.
		err = s.Run(ctx, "msiexec", "/i", installer, "/passive", "/norestart")
	case "linux":
		err = s.installPackage(ctx, installer, out)
	default:
		return "", s.unsupported()
	}
	if err != nil {
		return "", err
	}
	path, err := s.Locate()
	if err == nil && path == "" {
		err = errors.New("the installer finished, but Kstack is not where it installs")
	}
	return path, err
}

// installDiskImage copies Kstack.app from the disk image into ~/Applications.
func (s *System) installDiskImage(ctx context.Context, image string) error {
	mnt := filepath.Join(filepath.Dir(image), "mnt")
	if err := s.Run(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mnt, image); err != nil {
		return err
	}
	// Detached even when ctx is done, so a cancelled copy leaves no mount.
	// A failed detach only leaves the image mounted; the copy decides success.
	defer func() { _ = s.Run(context.WithoutCancel(ctx), "hdiutil", "detach", mnt) }()
	return s.Run(ctx, "ditto", filepath.Join(mnt, "Kstack.app"), filepath.Join(s.Home, "Applications", "Kstack.app"))
}

// installPackage installs a .deb with apt-get or an .rpm with dnf, through
// sudo unless this process is root. The command runs on the terminal, so the
// user sees sudo's prompt and the package manager's output.
func (s *System) installPackage(ctx context.Context, pkg string, out io.Writer) error {
	var args []string
	switch filepath.Ext(pkg) {
	case ".deb":
		args = []string{"apt-get", "install", "-y", pkg}
	case ".rpm":
		args = []string{"dnf", "install", "-y", pkg}
	default:
		return fmt.Errorf("kstack app cannot install %s", filepath.Base(pkg))
	}
	if !s.IsRoot {
		args = append([]string{"sudo"}, args...)
	}
	fmt.Fprintf(out, "Running %s\n", strings.Join(args, " "))
	return s.Interact(ctx, args[0], args[1:]...)
}
