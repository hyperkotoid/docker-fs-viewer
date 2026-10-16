package workspace

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/denis/docker-fs-viewer/internal/model"
)

var ignoredNames = map[string]bool{
	".DS_Store":               true,
	".DocumentRevisions-V100": true,
	".Spotlight-V100":         true,
	".TemporaryItems":         true,
	".Trashes":                true,
	".fseventsd":              true,
}

func IsIgnored(name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if ignoredNames[part] || strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}

func Extract(root, containerID string, source io.Reader) (*model.Manifest, string, error) {
	digest := sha256.New()
	input := io.TeeReader(source, digest)
	tr := tar.NewReader(input)
	manifest := &model.Manifest{
		Version:     model.SessionVersion,
		ContainerID: containerID,
		CreatedAt:   time.Now().UTC(),
		Entries:     make(map[string]*model.Entry),
	}
	var hardlinks []*model.Entry
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read exported tar: %w", err)
		}
		name, err := cleanTarPath(header.Name)
		if err != nil {
			return nil, "", err
		}
		if name == "" || IsIgnored(name) {
			continue
		}
		target, err := secureTarget(root, name)
		if err != nil {
			return nil, "", err
		}
		if err := ensureSafeParents(root, target); err != nil {
			return nil, "", err
		}
		entry := entryFromHeader(name, header)
		manifest.Entries[name] = entry

		switch header.Typeflag {
		case tar.TypeDir:
			if err := replaceForType(target, true); err != nil {
				return nil, "", err
			}
			if err := os.MkdirAll(target, os.FileMode(header.Mode&0o777)|0o700); err != nil {
				return nil, "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return nil, "", err
			}
			if err := removeIfPresent(target); err != nil {
				return nil, "", err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, "", err
			}
			hash := sha256.New()
			_, copyErr := io.Copy(io.MultiWriter(file, hash), tr)
			closeErr := file.Close()
			if copyErr != nil {
				return nil, "", copyErr
			}
			if closeErr != nil {
				return nil, "", closeErr
			}
			entry.SHA256 = hex.EncodeToString(hash.Sum(nil))
			_ = os.Chmod(target, os.FileMode(header.Mode&0o777)|0o600)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return nil, "", err
			}
			if err := removeIfPresent(target); err != nil {
				return nil, "", err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return nil, "", err
			}
		case tar.TypeLink:
			hardlinks = append(hardlinks, entry)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			entry.Virtual = true
		default:
			entry.Virtual = true
		}
	}
	if _, err := io.Copy(io.Discard, input); err != nil {
		return nil, "", err
	}
	for _, entry := range hardlinks {
		target, _ := secureTarget(root, entry.Name)
		linkName, err := cleanTarPath(entry.LinkName)
		if err != nil {
			return nil, "", err
		}
		sourcePath, err := secureTarget(root, linkName)
		if err != nil {
			return nil, "", err
		}
		if err := ensureSafeParents(root, target); err != nil {
			return nil, "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, "", err
		}
		if err := removeIfPresent(target); err != nil {
			return nil, "", err
		}
		if err := os.Link(sourcePath, target); err != nil {
			return nil, "", fmt.Errorf("create hardlink %s -> %s: %w", entry.Name, entry.LinkName, err)
		}
		if hash, err := hashFile(target); err == nil {
			entry.SHA256 = hash
		}
	}
	return manifest, hex.EncodeToString(digest.Sum(nil)), nil
}

func Digest(source io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, source); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func Scan(root string) (map[string]*model.Entry, error) {
	entries := make(map[string]*model.Entry)
	err := filepath.WalkDir(root, func(current string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if IsIgnored(name) {
			if item.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		entry := &model.Entry{Name: name, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
		switch {
		case info.Mode().IsDir():
			entry.Type = tar.TypeDir
		case info.Mode()&os.ModeSymlink != 0:
			entry.Type = tar.TypeSymlink
			entry.LinkName, err = os.Readlink(current)
			if err != nil {
				return err
			}
		case info.Mode().IsRegular():
			entry.Type = tar.TypeReg
			entry.Size = info.Size()
			entry.SHA256, err = hashFile(current)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported host file type at %s", name)
		}
		entries[name] = entry
		return nil
	})
	return entries, err
}

func BuildTar(root, output string, manifest *model.Manifest) error {
	current, err := Scan(root)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(file)
	closeWithError := func(cause error) error {
		_ = tw.Close()
		_ = file.Close()
		return cause
	}

	names := make([]string, 0, len(current)+len(manifest.Entries))
	seen := make(map[string]bool)
	for name := range current {
		names = append(names, name)
		seen[name] = true
	}
	for name, baseline := range manifest.Entries {
		if baseline.Virtual && !seen[name] && !hasDeletedAncestor(name, current, manifest) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		hostEntry, exists := current[name]
		baseline := manifest.Entries[name]
		if !exists && (baseline == nil || !baseline.Virtual) {
			continue
		}
		var header *tar.Header
		if baseline != nil && compatibleType(baseline.Type, hostEntry, exists) {
			header = headerFromEntry(baseline)
		} else {
			uid, gid := inheritedOwner(name, manifest)
			header = headerFromEntry(hostEntry)
			header.Uid, header.Gid = uid, gid
			header.ModTime = time.Now()
		}
		header.Name = name
		if exists {
			switch hostEntry.Type {
			case tar.TypeDir:
				header.Typeflag = tar.TypeDir
				header.Size = 0
			case tar.TypeSymlink:
				header.Typeflag = tar.TypeSymlink
				header.Linkname = hostEntry.LinkName
				header.Size = 0
			case tar.TypeReg:
				if baseline == nil || baseline.Type != tar.TypeLink || !sameFile(root, name, baseline.LinkName) {
					header.Typeflag = tar.TypeReg
					header.Linkname = ""
					header.Size = hostEntry.Size
				}
			}
		}
		if err := tw.WriteHeader(header); err != nil {
			return closeWithError(err)
		}
		if exists && header.Typeflag == tar.TypeReg {
			input, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				return closeWithError(err)
			}
			_, copyErr := io.Copy(tw, input)
			closeErr := input.Close()
			if copyErr != nil {
				return closeWithError(copyErr)
			}
			if closeErr != nil {
				return closeWithError(closeErr)
			}
		}
	}
	if err := tw.Close(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func cleanTarPath(name string) (string, error) {
	name = strings.TrimPrefix(filepath.ToSlash(name), "./")
	name = strings.TrimSuffix(name, "/")
	cleaned := path.Clean(name)
	if cleaned == "." {
		return "", nil
	}
	if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.ContainsRune(cleaned, 0) {
		return "", fmt.Errorf("unsafe path in container export: %q", name)
	}
	return cleaned, nil
}

func secureTarget(root, name string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(name))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes workspace: %q", name)
	}
	return target, nil
}

func ensureSafeParents(root, target string) error {
	parent := filepath.Dir(target)
	relative, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	current := root
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("unsafe parent in container export: %s", current)
		}
	}
	return nil
}

func replaceForType(target string, directory bool) error {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if directory && info.IsDir() {
		return nil
	}
	return os.RemoveAll(target)
}

func removeIfPresent(target string) error {
	_, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(target)
}

func hashFile(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sameFile(root, first, second string) bool {
	firstInfo, err := os.Stat(filepath.Join(root, filepath.FromSlash(first)))
	if err != nil {
		return false
	}
	secondName, err := cleanTarPath(second)
	if err != nil || secondName == "" {
		return false
	}
	secondInfo, err := os.Stat(filepath.Join(root, filepath.FromSlash(secondName)))
	return err == nil && os.SameFile(firstInfo, secondInfo)
}

func entryFromHeader(name string, header *tar.Header) *model.Entry {
	return &model.Entry{
		Name: name, Mode: header.Mode, UID: header.Uid, GID: header.Gid,
		Size: header.Size, ModTime: header.ModTime, AccessTime: header.AccessTime,
		ChangeTime: header.ChangeTime, Type: header.Typeflag, LinkName: header.Linkname,
		Uname: header.Uname, Gname: header.Gname, DevMajor: header.Devmajor,
		DevMinor: header.Devminor, Xattrs: cloneMap(header.Xattrs),
		PAXRecords: cloneMap(header.PAXRecords), Format: int(header.Format),
	}
}

func headerFromEntry(entry *model.Entry) *tar.Header {
	return &tar.Header{
		Name: entry.Name, Mode: entry.Mode, Uid: entry.UID, Gid: entry.GID,
		Size: entry.Size, ModTime: entry.ModTime, AccessTime: entry.AccessTime,
		ChangeTime: entry.ChangeTime, Typeflag: entry.Type, Linkname: entry.LinkName,
		Uname: entry.Uname, Gname: entry.Gname, Devmajor: entry.DevMajor,
		Devminor: entry.DevMinor, Xattrs: cloneMap(entry.Xattrs),
		PAXRecords: cloneMap(entry.PAXRecords), Format: tar.Format(entry.Format),
	}
}

func compatibleType(baseline byte, current *model.Entry, exists bool) bool {
	if !exists {
		return baseline == tar.TypeChar || baseline == tar.TypeBlock || baseline == tar.TypeFifo
	}
	if baseline == tar.TypeLink {
		return current.Type == tar.TypeReg
	}
	if baseline == tar.TypeRegA {
		return current.Type == tar.TypeReg
	}
	return baseline == current.Type
}

func inheritedOwner(name string, manifest *model.Manifest) (int, int) {
	parent := path.Dir(name)
	for parent != "." && parent != "/" {
		if entry := manifest.Entries[parent]; entry != nil {
			return entry.UID, entry.GID
		}
		parent = path.Dir(parent)
	}
	return 0, 0
}

func hasDeletedAncestor(name string, current map[string]*model.Entry, manifest *model.Manifest) bool {
	parent := path.Dir(name)
	for parent != "." && parent != "/" {
		if baseline := manifest.Entries[parent]; baseline != nil && !baseline.Virtual && current[parent] == nil {
			return true
		}
		parent = path.Dir(parent)
	}
	return false
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
