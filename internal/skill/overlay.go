package skill

import (
	"bytes"
	"fmt"
	"io/fs"
	"time"
)

func withMetadata(skill fs.FS, meta SourceMetadata) (fs.FS, error) {
	data, err := fs.ReadFile(skill, "SKILL.md")
	if err != nil {
		return nil, err
	}

	injected, err := InjectSourceMetadata(string(data), meta)
	if err != nil {
		return nil, fmt.Errorf("SKILL.md: %w", err)
	}

	return overlayFS{FS: skill, skillMD: []byte(injected)}, nil
}

type overlayFS struct {
	fs.FS
	skillMD []byte
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if name != "SKILL.md" {
		return o.FS.Open(name)
	}

	return &memFile{Reader: bytes.NewReader(o.skillMD), size: int64(len(o.skillMD))}, nil
}

func (o overlayFS) ReadLink(name string) (string, error) { return fs.ReadLink(o.FS, name) }

func (o overlayFS) Lstat(name string) (fs.FileInfo, error) {
	if name == "SKILL.md" {
		return &memFile{size: int64(len(o.skillMD))}, nil
	}

	return fs.Lstat(o.FS, name)
}

type memFile struct {
	*bytes.Reader
	size int64
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *memFile) Close() error               { return nil }
func (f *memFile) Name() string               { return "SKILL.md" }
func (f *memFile) Size() int64                { return f.size }
func (f *memFile) Mode() fs.FileMode          { return 0o644 }
func (f *memFile) ModTime() time.Time         { return time.Time{} }
func (f *memFile) IsDir() bool                { return false }
func (f *memFile) Sys() any                   { return nil }
