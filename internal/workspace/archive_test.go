package workspace

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractAndBuildTarPreservesMetadataAndChanges(t *testing.T) {
	source := tarBytes(t, []tarItem{
		{header: tar.Header{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o755, Uid: 0, Gid: 0}},
		{header: tar.Header{Name: "etc/config", Typeflag: tar.TypeReg, Mode: 0o640, Uid: 42, Gid: 43, Size: 3}, body: "old"},
		{header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "etc/config"}},
		{header: tar.Header{Name: "dev/console", Typeflag: tar.TypeChar, Mode: 0o600, Devmajor: 5, Devminor: 1}},
	})
	root := t.TempDir()
	manifest, digest, err := Extract(root, "container", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || manifest.Entries["etc/config"].UID != 42 {
		t.Fatalf("metadata not captured: %#v", manifest.Entries["etc/config"])
	}
	if !manifest.Entries["dev/console"].Virtual {
		t.Fatal("special file should be virtual")
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "config"), []byte("new value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(t.TempDir(), "rootfs.tar")
	if err := BuildTar(root, output, manifest); err != nil {
		t.Fatal(err)
	}
	headers := readTar(t, output)
	config := headers["etc/config"]
	if config == nil || config.Uid != 42 || config.Gid != 43 || config.Mode != 0o640 || config.Size != 9 {
		t.Fatalf("existing metadata was not preserved: %#v", config)
	}
	if _, ok := headers["link"]; ok {
		t.Fatal("deleted symlink was emitted")
	}
	if added := headers["added"]; added == nil || added.Uid != 0 || added.Gid != 0 {
		t.Fatalf("new file should inherit root ownership: %#v", added)
	}
	if special := headers["dev/console"]; special == nil || special.Typeflag != tar.TypeChar {
		t.Fatalf("virtual special file was not preserved: %#v", special)
	}
}

func TestExtractRejectsSymlinkParentTraversal(t *testing.T) {
	source := tarBytes(t, []tarItem{
		{header: tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}},
		{header: tar.Header{Name: "escape/payload", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}, body: "x"},
	})
	_, _, err := Extract(t.TempDir(), "container", bytes.NewReader(source))
	if err == nil {
		t.Fatal("expected symlink-parent traversal to be rejected")
	}
}

func TestExtractRejectsDotDot(t *testing.T) {
	source := tarBytes(t, []tarItem{
		{header: tar.Header{Name: "../outside", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}, body: "x"},
	})
	_, _, err := Extract(t.TempDir(), "container", bytes.NewReader(source))
	if err == nil {
		t.Fatal("expected parent traversal to be rejected")
	}
}

type tarItem struct {
	header tar.Header
	body   string
}

func tarBytes(t *testing.T, items []tarItem) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, item := range items {
		header := item.header
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, item.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func readTar(t *testing.T, fileName string) map[string]*tar.Header {
	t.Helper()
	file, err := os.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result := make(map[string]*tar.Header)
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		copy := *header
		result[header.Name] = &copy
	}
	return result
}
