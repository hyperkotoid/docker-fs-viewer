package session

import (
	"testing"
	"time"

	"github.com/denis/docker-fs-viewer/internal/model"
)

func TestSaveLoadAndResolve(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	first := &model.Session{
		Version: 1, ID: "abcdef-first", ContainerID: "abcdef123456",
		ContainerName: "/demo", CreatedAt: time.Now().Add(-time.Hour), State: "ready",
	}
	second := &model.Session{
		Version: 1, ID: "fedcba-second", ContainerID: "fedcba123456",
		ContainerName: "/other", CreatedAt: time.Now(), State: "applied",
	}
	if err := store.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(second); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Resolve("demo")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != first.ID {
		t.Fatalf("resolved %s, want %s", resolved.ID, first.ID)
	}
	latest, err := store.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID != second.ID {
		t.Fatalf("latest %s, want %s", latest.ID, second.ID)
	}
}
