package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Install installs the app from the installer Download returned, for this
// user alone, then removes the installer's directory. It needs no admin rights.
func (s *System) Install(ctx context.Context, installer string) (string, error) {
	defer os.RemoveAll(filepath.Dir(installer))
	var err error
	switch s.OS {
	case "darwin":
		err = s.installDiskImage(ctx, installer)
	case "windows":
		// NSIS's silent mode, which installs into %LOCALAPPDATA%.
		err = s.Run(ctx, installer, "/S")
	case "linux":
		err = s.installAppImage(installer)
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

// installAppImage copies the AppImage to ~/Applications and makes it executable.
func (s *System) installAppImage(image string) error {
	dir := filepath.Join(s.Home, "Applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	src, err := os.Open(image)
	if err != nil {
		return err
	}
	defer src.Close()
	dst := filepath.Join(dir, "Kstack.AppImage")
	if err := writeFile(dst, src); err != nil {
		return err
	}
	return os.Chmod(dst, 0o755)
}
