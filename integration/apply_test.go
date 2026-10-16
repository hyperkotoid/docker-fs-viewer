//go:build integration

package integration

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/denis/docker-fs-viewer/internal/app"
)

func TestMountApplyVolumeAndRollback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("APFS integration test requires macOS")
	}
	image := os.Getenv("DOCKER_FS_VIEWER_TEST_IMAGE")
	if image == "" {
		image = "nginx:1.27-alpine"
	}
	if exec.Command("docker", "image", "inspect", image).Run() != nil {
		t.Skipf("test image %s is not available locally", image)
	}
	suffix := time.Now().UTC().Format("150405.000000")
	name := "docker-fs-viewer-integration-" + strings.ReplaceAll(suffix, ".", "")
	volume := name + "-data"
	runDocker(t, "volume", "create", volume)
	runDocker(t, "create", "--name", name, "-v", volume+":/data", "-v", "/anonymous", image)
	fixture := t.TempDir()
	existingSource := filepath.Join(fixture, "existing")
	deleteSource := filepath.Join(fixture, "delete")
	if err := os.WriteFile(existingSource, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deleteSource, []byte("remove me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runDocker(t, "cp", existingSource, name+":/dfs-existing")
	runDocker(t, "cp", deleteSource, name+":/dfs-delete")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "-v", name).Run()
		_ = exec.Command("docker", "volume", "rm", volume).Run()
	})

	t.Setenv("DOCKER_FS_VIEWER_NO_FINDER", "1")
	ctx := context.Background()
	application, err := app.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mounted, err := application.Mount(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = application.Unmount(context.Background(), mounted.Session.ID, true)
		_ = os.RemoveAll(application.Sessions.Dir(mounted.Session.ID))
	})
	if len(mounted.ExternalMounts) != 2 {
		t.Fatalf("external volume not reported: %#v", mounted.ExternalMounts)
	}
	originalVolumes := map[string]string{}
	for _, mount := range mounted.ExternalMounts {
		originalVolumes[mount.Destination] = mount.Name
	}
	if originalVolumes["/data"] != volume || originalVolumes["/anonymous"] == "" {
		t.Fatalf("volume identities not captured: %#v", originalVolumes)
	}
	if err := os.WriteFile(filepath.Join(mounted.Session.MountPoint, "integration-added"), []byte("works\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mounted.Session.MountPoint, "dfs-existing"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(mounted.Session.MountPoint, "dfs-delete")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dfs-existing", filepath.Join(mounted.Session.MountPoint, "dfs-link")); err != nil {
		t.Fatal(err)
	}
	applied, changes, err := application.Apply(ctx, mounted.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 || applied.ReplacementID == "" {
		t.Fatalf("unexpected apply result: %#v %#v", applied, changes)
	}
	replacement, _, err := application.Docker.Inspect(ctx, applied.ReplacementID)
	if err != nil {
		t.Fatal(err)
	}
	replacementVolumes := map[string]string{}
	for _, mount := range replacement.Mounts {
		replacementVolumes[mount.Destination] = mount.Name
	}
	if replacementVolumes["/data"] != originalVolumes["/data"] ||
		replacementVolumes["/anonymous"] != originalVolumes["/anonymous"] {
		t.Fatalf("volume was not reattached: %#v", replacement.Mounts)
	}
	export, err := application.Docker.Export(ctx, applied.ReplacementID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]*tar.Header{}
	bodies := map[string]string{}
	reader := tar.NewReader(export)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entryName := strings.TrimPrefix(header.Name, "./")
		if entryName == "integration-added" || entryName == "dfs-existing" || entryName == "dfs-delete" || entryName == "dfs-link" {
			copy := *header
			found[entryName] = &copy
			if header.Typeflag == tar.TypeReg {
				data, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				bodies[entryName] = string(data)
			}
		}
	}
	_ = export.Close()
	if found["integration-added"] == nil || bodies["integration-added"] != "works\n" {
		t.Fatal("added file is missing from replacement rootfs")
	}
	if found["dfs-existing"] == nil || bodies["dfs-existing"] != "after\n" {
		t.Fatal("edited file was not updated")
	}
	if found["dfs-delete"] != nil {
		t.Fatal("deleted file remains in replacement rootfs")
	}
	if link := found["dfs-link"]; link == nil || link.Typeflag != tar.TypeSymlink || link.Linkname != "dfs-existing" {
		t.Fatalf("new symlink was not preserved: %#v", link)
	}
	if _, err := application.Rollback(ctx, mounted.Session.ID); err != nil {
		t.Fatal(err)
	}
	original, _, err := application.Docker.Inspect(ctx, mounted.Session.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimPrefix(original.Name, "/") != name {
		t.Fatalf("original name was not restored: %s", original.Name)
	}
}

func runDocker(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("docker", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
