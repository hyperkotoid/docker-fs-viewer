package diff

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/denis/docker-fs-viewer/internal/model"
)

func TestCompare(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("same", "same")
	write("changed", "new")
	write("added", "added")
	if err := os.WriteFile(filepath.Join(root, ".DS_Store"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := &model.Manifest{Entries: map[string]*model.Entry{
		"same":    {Name: "same", Type: tar.TypeReg, Size: 4, SHA256: hash("same")},
		"changed": {Name: "changed", Type: tar.TypeReg, Size: 3, SHA256: hash("old")},
		"deleted": {Name: "deleted", Type: tar.TypeReg, Size: 3, SHA256: hash("old")},
		"device":  {Name: "device", Type: tar.TypeChar, Virtual: true},
	}}
	changes, err := Compare(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %#v", changes)
	}
	got := map[string]string{}
	for _, change := range changes {
		got[change.Path] = change.Kind
	}
	if got["added"] != "added" || got["changed"] != "modified" || got["deleted"] != "deleted" {
		t.Fatalf("unexpected changes: %#v", got)
	}
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
