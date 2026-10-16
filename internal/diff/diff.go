package diff

import (
	"archive/tar"
	"sort"

	"github.com/denis/docker-fs-viewer/internal/model"
	"github.com/denis/docker-fs-viewer/internal/workspace"
)

func Compare(root string, manifest *model.Manifest) ([]model.Change, error) {
	current, err := workspace.Scan(root)
	if err != nil {
		return nil, err
	}
	var changes []model.Change
	for name, baseline := range manifest.Entries {
		item, exists := current[name]
		if !exists {
			if !baseline.Virtual {
				changes = append(changes, model.Change{Path: name, Kind: "deleted"})
			}
			continue
		}
		if modified(baseline, item) {
			changes = append(changes, model.Change{Path: name, Kind: "modified"})
		}
	}
	for name := range current {
		if manifest.Entries[name] == nil {
			changes = append(changes, model.Change{Path: name, Kind: "added"})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Kind < changes[j].Kind
		}
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

func modified(baseline, current *model.Entry) bool {
	expectedType := baseline.Type
	if expectedType == tar.TypeLink || expectedType == tar.TypeRegA {
		expectedType = tar.TypeReg
	}
	if expectedType != current.Type {
		return true
	}
	switch current.Type {
	case tar.TypeReg:
		if baseline.Type == tar.TypeLink {
			return baseline.SHA256 != "" && baseline.SHA256 != current.SHA256
		}
		return baseline.Size != current.Size || (baseline.SHA256 != "" && baseline.SHA256 != current.SHA256)
	case tar.TypeSymlink:
		return baseline.LinkName != current.LinkName
	default:
		return false
	}
}
