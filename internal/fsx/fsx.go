// Package fsx performs filesystem operations strictly confined to a root
// directory. Logical paths are "/"-rooted relative to that root.
package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	// ModTimeMs is the same instant as ModTime in unix milliseconds —
	// a number clients can sort/subtract without parsing a string.
	ModTimeMs int64 `json:"mod_time_ms"`
}

// stamp keeps ModTimeMs in step with ModTime; called wherever an Entry
// is built from a FileInfo.
func (e *Entry) stamp() { e.ModTimeMs = e.ModTime.UnixMilli() }

var (
	ErrExists        = errors.New("already exists")
	ErrNotDir        = errors.New("not a directory")
	ErrNotEmpty      = errors.New("directory not empty")
	ErrParentMissing = errors.New("parent does not exist")
)

// Tree is a sandbox rooted at an absolute directory.
type Tree struct{ root string }

// New resolves root to an absolute path, requires it to exist, and creates
// it if missing (unless it exists as a file).
func New(root string) (*Tree, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Tree{root: abs}, nil
}

func (t *Tree) Root() string { return t.root }

// Abs maps a cleaned logical path to a real path inside the root and
// rejects any traversal through symlinks.
func (t *Tree) Abs(logical string) (string, error) {
	if !strings.HasPrefix(logical, "/") {
		return "", fmt.Errorf("logical path must start with /")
	}
	abs := filepath.Join(t.root, filepath.FromSlash(logical))
	if abs != t.root && !strings.HasPrefix(abs, t.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes root")
	}
	// Reject symlinks in any component so files outside root stay unreachable.
	cur := t.root
	for _, part := range strings.Split(strings.Trim(logical, "/"), "/") {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinks are not permitted")
		}
	}
	return abs, nil
}

// List returns the entries of a directory, sorted by name.
func (t *Tree) List(logical string) ([]Entry, error) {
	abs, err := t.Abs(logical)
	if err != nil {
		return nil, err
	}
	ds, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		if errors.Is(err, syscall.ENOTDIR) {
			return nil, ErrNotDir
		}
		return nil, err
	}
	out := make([]Entry, 0, len(ds))
	for _, de := range ds {
		fi, err := de.Info()
		if err != nil {
			continue // skip unreadable entries rather than failing the list
		}
		child := joinLogical(logical, de.Name())
		e := Entry{
			Name: de.Name(), Path: child, IsDir: de.IsDir(),
			Size: fi.Size(), ModTime: fi.ModTime(),
		}
		e.stamp()
		out = append(out, e)
	}
	return out, nil
}

// ReadText returns the file contents (caller enforces size limits first).
func (t *Tree) ReadText(logical string) ([]byte, error) {
	abs, err := t.Abs(logical)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// Stat returns metadata for one path.
func (t *Tree) Stat(logical string) (Entry, error) {
	abs, err := t.Abs(logical)
	if err != nil {
		return Entry{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	name := filepath.Base(abs)
	if logical == "/" {
		name = "/"
	}
	e := Entry{Name: name, Path: logical, IsDir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime()}
	e.stamp()
	return e, nil
}

// Write creates or truncates a file, creating parent directories as needed.
func (t *Tree) Write(logical string, data []byte) error {
	abs, err := t.Abs(logical)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

// Mkdir creates a directory (single level; parent must exist).
func (t *Tree) Mkdir(logical string) error {
	abs, err := t.Abs(logical)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return ErrExists
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrParentMissing
		}
		return err
	}
	return nil
}

// Rename moves src to dst; dst must not exist.
func (t *Tree) Rename(from, to string) error {
	src, err := t.Abs(from)
	if err != nil {
		return err
	}
	dst, err := t.Abs(to)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return ErrExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// Delete removes a file, or an empty directory.
func (t *Tree) Delete(logical string) error {
	abs, err := t.Abs(logical)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		if des, err := os.ReadDir(abs); err == nil && len(des) > 0 {
			return ErrNotEmpty
		}
		return os.Remove(abs)
	}
	return os.Remove(abs)
}

func joinLogical(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}
