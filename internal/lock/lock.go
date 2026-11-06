// Package lock は vercel-labs/skills と共有するロックファイル skills-lock.json を読み書きする
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName は標準のロックファイル名である (vercel-labs/skills と互換)
const FileName = "skills-lock.json"

// DefaultDir はロックファイルが記録するスキルディレクトリである
const DefaultDir = ".agents/skills"

// File はロックファイルの内容を表す
type File struct {
	Version int              `json:"version"`
	Skills  map[string]Entry `json:"skills"`
}

// SourceType はスキルのインストール元リポジトリの種類である
type SourceType string

// vercel-labs/skills が記録するソースの種類
const (
	SourceGitHub SourceType = "github"
	SourceGitLab SourceType = "gitlab"
	SourceGit    SourceType = "git"
	SourceLocal  SourceType = "local"
)

// Supported はこのソースの種類のスキルを clone して更新できるかを返す
func (t SourceType) Supported() bool {
	return t == SourceGitHub || t == SourceGitLab || t == SourceGit || t == SourceLocal
}

// Entry はロックファイル内の単一のスキルを表す
type Entry struct {
	Source       string     `json:"source"`
	Ref          string     `json:"ref,omitempty"`
	SourceType   SourceType `json:"sourceType"`
	SkillPath    string     `json:"skillPath,omitempty"`
	ComputedHash string     `json:"computedHash"`
}

// ReadFile は path のロックファイルを読む
func ReadFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("ロックファイル %q の読み取り: %w", path, err)
	}

	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("ロックファイル %q のデコード: %w", path, err)
	}

	if file.Skills == nil {
		file.Skills = map[string]Entry{}
	}

	return file, nil
}

// WriteFile はロックファイルをアトミックに書き込む
func WriteFile(path string, file File) error {
	if file.Skills == nil {
		file.Skills = map[string]Entry{}
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("ロックファイル %q のエンコード: %w", path, err)
	}

	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ロックファイル %q の親ディレクトリの作成: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".skills-lock-*.json")
	if err != nil {
		return fmt.Errorf("%q の一時ロックファイルの作成: %w", path, err)
	}

	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o644); err != nil {
		return errors.Join(fmt.Errorf("%q の一時ロックファイルの chmod: %w", path, err), tmp.Close())
	}

	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("%q の一時ロックファイルの書き込み: %w", path, err), tmp.Close())
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%q の一時ロックファイルのクローズ: %w", path, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ロックファイル %q の置換: %w", path, err)
	}

	return nil
}

// Layout はインストール済みスキルとロックファイルのディレクトリ構造を表す
type Layout struct {
	Dir string
}

// NewLayout はスキルディレクトリ skillsDir のレイアウトを返す
func NewLayout(skillsDir string) Layout {
	return Layout{Dir: filepath.Clean(skillsDir)}
}

// LockPath はロックファイルのパスを返す。絶対パスのスキルディレクトリなら二階層上
// (.agents/skills ではプロジェクトルート)、相対パスなら
// 作業ディレクトリである
func (l Layout) LockPath() string {
	if filepath.IsAbs(l.Dir) {
		return filepath.Join(filepath.Dir(filepath.Dir(l.Dir)), FileName)
	}

	return FileName
}

// SkillDir はインストール済みスキル name のディレクトリを返す
func (l Layout) SkillDir(name string) string {
	return filepath.Join(l.Dir, name)
}

// FilePath は skillsDir のロックファイルのパスを返す
func FilePath(skillsDir string) string {
	return NewLayout(skillsDir).LockPath()
}

// UsesLockfile はロックファイルがこのレイアウトのディレクトリを記録するかを返す。
// これが成り立つのはロックファイルの隣にある DefaultDir だけである
func (l Layout) UsesLockfile() bool {
	rel, err := filepath.Rel(filepath.Dir(l.LockPath()), filepath.Clean(l.Dir))
	return err == nil && rel == filepath.Clean(DefaultDir)
}

// ReadLockFile はレイアウトのロックファイルを読む。レイアウトがロックファイルを
// 使わない場合やまだ存在しない場合は空のものを返す
func (l Layout) ReadLockFile() (File, error) {
	if !l.UsesLockfile() {
		return File{Version: 1, Skills: map[string]Entry{}}, nil
	}

	f, err := ReadFile(l.LockPath())
	if errors.Is(err, os.ErrNotExist) {
		return File{Version: 1, Skills: map[string]Entry{}}, nil
	}

	return f, err
}

// Entries はロックファイルがこのレイアウトのディレクトリを記録している場合に
// そのスキルを返し、そうでなければ何も返さない
func (l Layout) Entries(f File) map[string]Entry {
	if !l.UsesLockfile() {
		return map[string]Entry{}
	}

	return f.Skills
}
