package git

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tree はキャッシュ内の git ツリーを読む読み取り専用の fs.FS で、ツリーオブジェクトは
// たどる時点で読む。シンボリックリンクはたどらない。開こうとすると
// 失敗し、Stat と ReadDir はリンクとして報告する。サブモジュールは
// 通常でないファイルとして現れる。サイズを報告するのは開いたファイルだけである
type Tree struct {
	repo *repo
	root string

	mu   sync.Mutex
	dirs map[string][]treeEntry
}

type treeEntry struct {
	name string
	mode fs.FileMode
	oid  string
}

func (r *repo) tree(oid string) *Tree {
	return &Tree{repo: r, root: oid, dirs: map[string][]treeEntry{}}
}

// Open は通常ファイルまたはディレクトリ name を開く
func (t *Tree) Open(name string) (fs.File, error) {
	e, err := t.lookup("open", name)
	if err != nil {
		return nil, err
	}

	switch {
	case e.mode.IsDir():
		entries, err := t.ReadDir(name)
		if err != nil {
			return nil, err
		}

		return &treeDir{info: t.info(e), entries: entries}, nil
	case e.mode.IsRegular():
		data, err := t.repo.contents(e.oid)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}

		info := t.info(e)
		info.size = int64(len(data))

		return &treeFile{info: info, Reader: bytes.NewReader(data)}, nil
	}

	return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("通常ファイルでもディレクトリでもありません")}
}

// ReadFile は通常ファイル name を読む
func (t *Tree) ReadFile(name string) ([]byte, error) {
	e, err := t.lookup("read", name)
	if err != nil {
		return nil, err
	}

	if !e.mode.IsRegular() {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("通常ファイルではありません")}
	}

	return t.repo.contents(e.oid)
}

// ReadDir はディレクトリ name の内容を名前順に返す
func (t *Tree) ReadDir(name string) ([]fs.DirEntry, error) {
	e, err := t.lookup("readdir", name)
	if err != nil {
		return nil, err
	}

	if !e.mode.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errors.New("ディレクトリではありません")}
	}

	children, err := t.list(name, e.oid)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}

	entries := make([]fs.DirEntry, len(children))
	for i, c := range children {
		entries[i] = fs.FileInfoToDirEntry(t.info(c))
	}

	return entries, nil
}

// Stat はシンボリックリンクをたどらずに name の情報を返す
func (t *Tree) Stat(name string) (fs.FileInfo, error) {
	e, err := t.lookup("stat", name)
	if err != nil {
		return nil, err
	}

	return t.info(e), nil
}

// ReadLink はシンボリックリンク name の参照先を返す
func (t *Tree) ReadLink(name string) (string, error) {
	e, err := t.lookup("readlink", name)
	if err != nil {
		return "", err
	}

	if e.mode&fs.ModeSymlink == 0 {
		return "", &fs.PathError{Op: "readlink", Path: name, Err: errors.New("シンボリックリンクではありません")}
	}

	target, err := t.repo.contents(e.oid)

	return string(target), err
}

// Lstat は name の情報を返す。Stat もリンクをたどらない
func (t *Tree) Lstat(name string) (fs.FileInfo, error) {
	return t.Stat(name)
}

func (t *Tree) treeSHA(dir string) (string, error) {
	e, err := t.lookup("stat", dir)
	if err != nil {
		return "", err
	}

	if !e.mode.IsDir() {
		return "", fmt.Errorf("%s: ディレクトリではありません", dir)
	}

	return e.oid, nil
}

func (t *Tree) lookup(op, name string) (treeEntry, error) {
	if !fs.ValidPath(name) {
		return treeEntry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}

	e := treeEntry{name: ".", mode: fs.ModeDir | 0o755, oid: t.root}
	dir := "."

	for part := range strings.SplitSeq(name, "/") {
		if part == "." {
			continue
		}

		if !e.mode.IsDir() {
			return treeEntry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
		}

		children, err := t.list(dir, e.oid)
		if err != nil {
			return treeEntry{}, &fs.PathError{Op: op, Path: name, Err: err}
		}

		i, found := slices.BinarySearchFunc(children, part, func(c treeEntry, n string) int { return strings.Compare(c.name, n) })
		if !found {
			return treeEntry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
		}

		e, dir = children[i], path.Join(dir, part)
	}

	return e, nil
}

func (t *Tree) list(dir, oid string) ([]treeEntry, error) {
	t.mu.Lock()
	children, ok := t.dirs[dir]
	t.mu.Unlock()

	if ok {
		return children, nil
	}

	data, err := t.repo.contents(oid)
	if err != nil {
		return nil, err
	}

	children, err = parseTree(data, len(t.root)/2)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	t.dirs[dir] = children
	t.mu.Unlock()

	return children, nil
}

func parseTree(data []byte, hashSize int) ([]treeEntry, error) {
	var entries []treeEntry

	for len(data) > 0 {
		sp := bytes.IndexByte(data, ' ')
		nul := bytes.IndexByte(data, 0)

		if sp < 0 || nul < sp || len(data) < nul+1+hashSize {
			return nil, errors.New("ツリーオブジェクトの形式が不正です")
		}

		entries = append(entries, treeEntry{
			name: string(data[sp+1 : nul]),
			mode: fileMode(string(data[:sp])),
			oid:  hex.EncodeToString(data[nul+1 : nul+1+hashSize]),
		})
		data = data[nul+1+hashSize:]
	}

	slices.SortFunc(entries, func(a, b treeEntry) int { return strings.Compare(a.name, b.name) })

	return entries, nil
}

func fileMode(gitMode string) fs.FileMode {
	mode, _ := strconv.ParseUint(gitMode, 8, 32)

	switch mode {
	case 0o40000:
		return fs.ModeDir | 0o755
	case 0o100755:
		return 0o755
	case 0o100644:
		return 0o644
	case 0o120000:
		return fs.ModeSymlink | 0o777
	default:
		return fs.ModeIrregular
	}
}

func (t *Tree) info(e treeEntry) treeInfo {
	return treeInfo{name: path.Base(e.name), mode: e.mode}
}

type treeInfo struct {
	name string
	mode fs.FileMode
	size int64
}

func (i treeInfo) Name() string       { return i.name }
func (i treeInfo) Size() int64        { return i.size }
func (i treeInfo) Mode() fs.FileMode  { return i.mode }
func (i treeInfo) ModTime() time.Time { return time.Time{} }
func (i treeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i treeInfo) Sys() any           { return nil }

type treeFile struct {
	info treeInfo
	*bytes.Reader
}

func (f *treeFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *treeFile) Close() error               { return nil }

type treeDir struct {
	info    treeInfo
	entries []fs.DirEntry
}

func (d *treeDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *treeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: errors.New("ディレクトリです")}
}
func (d *treeDir) Close() error { return nil }

func (d *treeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		entries := d.entries
		d.entries = nil

		return entries, nil
	}

	if len(d.entries) == 0 {
		return nil, io.EOF
	}

	n = min(n, len(d.entries))
	entries := d.entries[:n]
	d.entries = d.entries[n:]

	return entries, nil
}
