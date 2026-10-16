package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/denis/docker-fs-viewer/internal/apply"
	"github.com/denis/docker-fs-viewer/internal/diff"
	"github.com/denis/docker-fs-viewer/internal/docker"
	"github.com/denis/docker-fs-viewer/internal/model"
	"github.com/denis/docker-fs-viewer/internal/session"
	"github.com/denis/docker-fs-viewer/internal/workspace"
)

type App struct {
	Docker    *docker.Client
	Sessions  *session.Store
	Workspace workspace.APFS
}

type MountResult struct {
	Session        *model.Session
	ExternalMounts []model.Mount
	FinderWarning  error
}

func New(ctx context.Context) (*App, error) {
	client, err := docker.New(ctx)
	if err != nil {
		return nil, err
	}
	store, err := session.NewStore()
	if err != nil {
		return nil, err
	}
	return &App{Docker: client, Sessions: store}, nil
}

func (a *App) List(ctx context.Context) ([]docker.ContainerSummary, error) {
	return a.Docker.List(ctx)
}

func (a *App) SessionsList() ([]*model.Session, error) {
	return a.Sessions.List()
}

func (a *App) Mount(ctx context.Context, containerRef string) (*MountResult, error) {
	inspect, raw, err := a.Docker.Inspect(ctx, containerRef)
	if err != nil {
		return nil, err
	}
	if inspect.State.Running {
		return nil, fmt.Errorf("container %s is running; stop it first", inspect.Name)
	}
	id := session.NewID(inspect.ID)
	dir := a.Sessions.Dir(id)
	item := &model.Session{
		Version: model.SessionVersion, ID: id, ContainerID: inspect.ID,
		ContainerName: inspect.Name, CreatedAt: time.Now().UTC(), State: "creating",
		Inspect: raw, ImagePath: workspace.ImagePath(dir), MountPoint: workspace.MountPath(dir),
	}
	if err := a.Sessions.Create(item); err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			if a.Workspace.IsMounted(context.Background(), item.MountPoint) {
				_ = a.Workspace.Detach(context.Background(), item.MountPoint, true)
			}
			_ = os.RemoveAll(dir)
		}
	}()

	size := inspect.SizeRootFS*2 + (1 << 30)
	volumeName := "Docker FS - " + strings.TrimPrefix(inspect.Name, "/")
	if err := a.Workspace.Create(ctx, item.ImagePath, volumeName, size); err != nil {
		return nil, err
	}
	if err := a.Workspace.Attach(ctx, item.ImagePath, item.MountPoint); err != nil {
		return nil, err
	}
	export, err := a.Docker.Export(ctx, inspect.ID)
	if err != nil {
		return nil, err
	}
	manifest, digest, extractErr := workspace.Extract(item.MountPoint, inspect.ID, export)
	closeErr := export.Close()
	if extractErr != nil {
		return nil, extractErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := a.Sessions.SaveManifest(item.ID, manifest); err != nil {
		return nil, err
	}
	item.BaseDigest = digest
	item.State = "ready"
	if err := a.Sessions.Save(item); err != nil {
		return nil, err
	}
	cleanup = false
	var finderErr error
	if os.Getenv("DOCKER_FS_VIEWER_NO_FINDER") == "" {
		finderErr = a.Workspace.OpenFinder(ctx, item.MountPoint)
	}
	return &MountResult{Session: item, ExternalMounts: inspect.Mounts, FinderWarning: finderErr}, nil
}

func (a *App) Status(ctx context.Context, ref string) (*model.Session, []model.Change, error) {
	item, manifest, err := a.loadWorkspace(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	changes, err := diff.Compare(item.MountPoint, manifest)
	return item, changes, err
}

func (a *App) Unmount(ctx context.Context, ref string, force bool) (*model.Session, error) {
	item, err := a.Sessions.Resolve(ref)
	if err != nil {
		return nil, err
	}
	if !a.Workspace.IsMounted(ctx, item.MountPoint) {
		return item, nil
	}
	if err := a.Workspace.Detach(ctx, item.MountPoint, force); err != nil {
		return nil, err
	}
	return item, a.Sessions.Save(item)
}

func (a *App) Apply(ctx context.Context, ref string) (*model.Session, []model.Change, error) {
	item, manifest, err := a.loadWorkspace(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	if item.State == "applied" {
		return nil, nil, fmt.Errorf("session is already applied; rollback it before applying again")
	}
	changes, err := diff.Compare(item.MountPoint, manifest)
	if err != nil {
		return nil, nil, err
	}
	if len(changes) == 0 {
		return item, changes, fmt.Errorf("workspace has no changes")
	}
	result, err := apply.Run(ctx, a.Docker, a.Sessions.Dir(item.ID), item.MountPoint, item, manifest)
	if err != nil {
		return nil, changes, err
	}
	item.State = "applied"
	item.ReplacementID = result.ReplacementID
	item.BackupName = result.BackupName
	item.ReplacementImage = result.Image
	if err := a.Sessions.Save(item); err != nil {
		return nil, changes, fmt.Errorf("replacement created, but session state could not be saved: %w", err)
	}
	return item, changes, nil
}

func (a *App) Rollback(ctx context.Context, ref string) (*model.Session, error) {
	item, err := a.Sessions.Resolve(ref)
	if err != nil {
		return nil, err
	}
	if err := apply.Rollback(ctx, a.Docker, item); err != nil {
		return nil, err
	}
	item.State = "rolled-back"
	item.ReplacementID = ""
	item.BackupName = ""
	item.ReplacementImage = ""
	return item, a.Sessions.Save(item)
}

func (a *App) ResolveSession(ref string) (*model.Session, error) {
	return a.Sessions.Resolve(ref)
}

func (a *App) loadWorkspace(ctx context.Context, ref string) (*model.Session, *model.Manifest, error) {
	item, err := a.Sessions.Resolve(ref)
	if err != nil {
		return nil, nil, err
	}
	if item.State == "creating" {
		return nil, nil, fmt.Errorf("session %s is incomplete", item.ID)
	}
	if !a.Workspace.IsMounted(ctx, item.MountPoint) {
		if _, err := os.Stat(item.ImagePath); err != nil {
			return nil, nil, fmt.Errorf("workspace image is missing: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(item.MountPoint), 0o700); err != nil {
			return nil, nil, err
		}
		if err := a.Workspace.Attach(ctx, item.ImagePath, item.MountPoint); err != nil {
			return nil, nil, err
		}
	}
	manifest, err := a.Sessions.LoadManifest(item.ID)
	if err != nil {
		return nil, nil, err
	}
	return item, manifest, nil
}
