package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/denis/docker-fs-viewer/internal/docker"
	"github.com/denis/docker-fs-viewer/internal/model"
	"github.com/denis/docker-fs-viewer/internal/workspace"
)

type Result struct {
	ReplacementID string
	BackupName    string
	Image         string
}

func Run(ctx context.Context, client *docker.Client, sessionDir, root string, session *model.Session, manifest *model.Manifest) (*Result, error) {
	inspect, raw, err := client.Inspect(ctx, session.ContainerID)
	if err != nil {
		return nil, fmt.Errorf("inspect source container: %w", err)
	}
	if inspect.State.Running {
		return nil, fmt.Errorf("container %s is running; stop it before apply", session.ContainerName)
	}
	if inspect.ID != session.ContainerID {
		return nil, fmt.Errorf("source container identity changed")
	}
	if err := validateConfig(inspect); err != nil {
		return nil, err
	}
	export, err := client.Export(ctx, inspect.ID)
	if err != nil {
		return nil, err
	}
	currentDigest, digestErr := workspace.Digest(export)
	closeErr := export.Close()
	if digestErr != nil {
		return nil, fmt.Errorf("verify source rootfs: %w", digestErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if currentDigest != session.BaseDigest {
		return nil, fmt.Errorf("source rootfs changed since mount; create a new session before applying")
	}

	tarPath := filepath.Join(sessionDir, "replacement-rootfs.tar")
	defer os.Remove(tarPath)
	if err := workspace.BuildTar(root, tarPath, manifest); err != nil {
		return nil, fmt.Errorf("build replacement rootfs: %w", err)
	}

	repository := "docker-fs-viewer/" + strings.ToLower(session.ID)
	tag := "latest"
	image := repository + ":" + tag
	if err := client.Import(ctx, tarPath, repository, tag); err != nil {
		return nil, fmt.Errorf("import replacement image: %w", err)
	}

	originalName := strings.TrimPrefix(session.ContainerName, "/")
	backupName := originalName + ".docker-fs-backup-" + time.Now().UTC().Format("20060102T150405")
	if err := client.Rename(ctx, inspect.ID, backupName); err != nil {
		_ = client.RemoveImage(ctx, image)
		return nil, fmt.Errorf("rename source container: %w", err)
	}

	createBody, err := replacementConfig(raw, inspect, image)
	if err != nil {
		_ = client.Rename(ctx, inspect.ID, originalName)
		_ = client.RemoveImage(ctx, image)
		return nil, err
	}
	replacementID, err := client.Create(ctx, originalName, createBody)
	if err != nil {
		renameErr := client.Rename(ctx, inspect.ID, originalName)
		_ = client.RemoveImage(ctx, image)
		if renameErr != nil {
			return nil, fmt.Errorf("create replacement: %v; additionally failed to restore source name: %w", err, renameErr)
		}
		return nil, fmt.Errorf("create replacement (source restored): %w", err)
	}
	return &Result{ReplacementID: replacementID, BackupName: backupName, Image: image}, nil
}

func Rollback(ctx context.Context, client *docker.Client, session *model.Session) error {
	if session.ReplacementID == "" || session.BackupName == "" {
		return fmt.Errorf("session has no applied replacement")
	}
	replacement, _, err := client.Inspect(ctx, session.ReplacementID)
	if err != nil {
		return fmt.Errorf("inspect replacement: %w", err)
	}
	if replacement.State.Running {
		return fmt.Errorf("replacement container is running; stop it before rollback")
	}
	if err := client.RemoveContainer(ctx, session.ReplacementID, false); err != nil {
		return fmt.Errorf("remove replacement: %w", err)
	}
	originalName := strings.TrimPrefix(session.ContainerName, "/")
	if err := client.Rename(ctx, session.ContainerID, originalName); err != nil {
		return fmt.Errorf("replacement was removed, but restoring backup name failed: %w", err)
	}
	if session.ReplacementImage != "" {
		_ = client.RemoveImage(ctx, session.ReplacementImage)
	}
	return nil
}

func validateConfig(inspect *model.Inspect) error {
	var host map[string]any
	if err := json.Unmarshal(inspect.HostConfig, &host); err != nil {
		return fmt.Errorf("decode HostConfig: %w", err)
	}
	for _, field := range []string{"NetworkMode", "IpcMode", "PidMode", "UTSMode"} {
		if mode, _ := host[field].(string); strings.HasPrefix(mode, "container:") {
			return fmt.Errorf("unsupported %s %q: it refers to another container namespace", field, mode)
		}
	}
	return nil
}

func replacementConfig(raw json.RawMessage, inspect *model.Inspect, image string) (map[string]any, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	var config map[string]any
	if err := json.Unmarshal(document["Config"], &config); err != nil {
		return nil, fmt.Errorf("decode Config: %w", err)
	}
	var hostConfig map[string]any
	if err := json.Unmarshal(document["HostConfig"], &hostConfig); err != nil {
		return nil, fmt.Errorf("decode HostConfig: %w", err)
	}
	reattachAnonymousVolumes(hostConfig, inspect.Mounts)
	config["Image"] = image
	config["HostConfig"] = hostConfig

	endpoints := make(map[string]any)
	oldName := strings.TrimPrefix(inspect.Name, "/")
	shortID := inspect.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	for networkName, endpoint := range inspect.NetworkSettings.Networks {
		aliases := make([]string, 0, len(endpoint.Aliases))
		for _, alias := range endpoint.Aliases {
			if alias != inspect.ID && alias != shortID && alias != oldName {
				aliases = append(aliases, alias)
			}
		}
		item := map[string]any{}
		if len(aliases) > 0 {
			item["Aliases"] = aliases
		}
		if endpoint.IPAMConfig != nil {
			item["IPAMConfig"] = endpoint.IPAMConfig
		}
		if len(endpoint.Links) > 0 {
			item["Links"] = endpoint.Links
		}
		if len(endpoint.DriverOpts) > 0 {
			item["DriverOpts"] = endpoint.DriverOpts
		}
		endpoints[networkName] = item
	}
	if len(endpoints) > 0 {
		config["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	}
	return config, nil
}

func reattachAnonymousVolumes(hostConfig map[string]any, mounts []model.Mount) {
	var binds []any
	if existing, ok := hostConfig["Binds"].([]any); ok {
		binds = append(binds, existing...)
	}
	targets := make(map[string]bool)
	for _, raw := range binds {
		value, _ := raw.(string)
		parts := strings.Split(value, ":")
		if len(parts) >= 2 {
			targets[parts[len(parts)-2]] = true
			if parts[len(parts)-1] == "rw" || parts[len(parts)-1] == "ro" ||
				strings.Contains(parts[len(parts)-1], ",") {
				if len(parts) >= 3 {
					targets[parts[len(parts)-2]] = true
				}
			} else {
				targets[parts[len(parts)-1]] = true
			}
		}
	}
	if structured, ok := hostConfig["Mounts"].([]any); ok {
		for _, raw := range structured {
			if item, ok := raw.(map[string]any); ok {
				if target, ok := item["Target"].(string); ok {
					targets[target] = true
				}
			}
		}
	}
	for _, mount := range mounts {
		if mount.Type != "volume" || mount.Name == "" || targets[mount.Destination] {
			continue
		}
		mode := "ro"
		if mount.RW {
			mode = "rw"
		}
		binds = append(binds, mount.Name+":"+mount.Destination+":"+mode)
		targets[mount.Destination] = true
	}
	if len(binds) > 0 {
		hostConfig["Binds"] = binds
	}
}
