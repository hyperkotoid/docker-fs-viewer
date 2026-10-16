//go:build darwin

package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

var volumeUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._ -]+`)

type APFS struct{}

func (APFS) Create(ctx context.Context, imagePath, volumeName string, size int64) error {
	if size < 2<<30 {
		size = 2 << 30
	}
	volumeName = volumeUnsafe.ReplaceAllString(volumeName, "_")
	args := []string{
		"create",
		"-size", fmt.Sprintf("%db", size),
		"-type", "SPARSEBUNDLE",
		"-fs", "Case-sensitive APFS",
		"-volname", volumeName,
		imagePath,
	}
	if output, err := exec.CommandContext(ctx, "hdiutil", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("create APFS workspace: %w: %s", err, output)
	}
	return nil
}

func (APFS) Attach(ctx context.Context, imagePath, mountPoint string) error {
	if err := os.MkdirAll(mountPoint, 0o700); err != nil {
		return err
	}
	args := []string{"attach", "-owners", "on", "-mountpoint", mountPoint, imagePath}
	if output, err := exec.CommandContext(ctx, "hdiutil", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("attach APFS workspace: %w: %s", err, output)
	}
	return nil
}

func (APFS) Detach(ctx context.Context, mountPoint string, force bool) error {
	args := []string{"detach"}
	if force {
		args = append(args, "-force")
	}
	args = append(args, mountPoint)
	if output, err := exec.CommandContext(ctx, "hdiutil", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("detach APFS workspace: %w: %s", err, output)
	}
	return nil
}

func (APFS) IsMounted(ctx context.Context, mountPoint string) bool {
	if mountPoint == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "diskutil", "info", "-plist", mountPoint)
	return cmd.Run() == nil
}

func (APFS) OpenFinder(ctx context.Context, mountPoint string) error {
	if output, err := exec.CommandContext(ctx, "open", mountPoint).CombinedOutput(); err != nil {
		return fmt.Errorf("open Finder: %w: %s", err, output)
	}
	return nil
}

func ImagePath(sessionDir string) string {
	return filepath.Join(sessionDir, "rootfs.sparsebundle")
}

func MountPath(sessionDir string) string {
	return filepath.Join(sessionDir, "mnt")
}
