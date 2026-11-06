package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
)

type skillReplacement struct {
	Name   string
	Source fs.FS
	Style  style
}

type stagedSkillReplacement struct {
	Name      string
	StagedDir string
}

type appliedSkillReplacement struct {
	Name      string
	Dir       string
	BackupDir string
}

type replacementTxn struct {
	layout  lock.Layout
	staged  []stagedSkillReplacement
	applied []appliedSkillReplacement
}

// InstallSkills は探索したスキルを skillsDir にコピーしてロックファイルを更新する
func InstallSkills(skills []DiscoveredSkill, ref SourceRef, layout lock.Layout) (err error) {
	if len(skills) == 0 {
		return errors.New("インストールするスキルがありません")
	}

	lf, err := layout.ReadLockFile()
	if err != nil {
		return err
	}

	replacements := make([]skillReplacement, len(skills))
	for i, s := range skills {
		hash, err := s.Hash()
		if err != nil {
			return fmt.Errorf("%q のハッシュの計算: %w", s.Name, err)
		}

		replacements[i] = skillReplacement{Name: s.Name, Source: s.FS, Style: vercelStyle}
		lf.Skills[s.Name] = lock.Entry{
			Source:       ref.CanonicalSource,
			Ref:          ref.Ref,
			SourceType:   ref.SourceType,
			SkillPath:    s.SkillMDPath(),
			ComputedHash: hash,
		}
	}

	tx, err := stageSkillReplacements(layout, replacements)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tx.Cleanup()) }()

	if err := tx.Apply(); err != nil {
		return err
	}

	if err := lock.WriteFile(layout.LockPath(), lf); err != nil {
		return errors.Join(fmt.Errorf("ロックファイルの書き込み: %w", err), tx.Rollback())
	}

	return nil
}

// InstallTrackedSkills は snap で探索したスキルをインストール先のディレクトリにコピーし、
// gh 互換のソースのメタデータを各 SKILL.md に注入する。ロックファイルは書かない。
// gh skill install と同様にフロントマターのメタデータがインストールの記録になる。
// 対応するのは github のソースだけである
func InstallTrackedSkills(skills []DiscoveredSkill, snap git.Snapshot, ref SourceRef, layout lock.Layout) (err error) {
	if len(skills) == 0 {
		return errors.New("インストールするスキルがありません")
	}

	if ref.SourceType != lock.SourceGitHub {
		return fmt.Errorf("gh skill 形式が対応するのは github のソースだけです (%q)", ref.SourceType)
	}

	replacements := make([]skillReplacement, len(skills))
	for i, s := range skills {
		treeSHA, err := snap.TreeSHA(s.Path)
		if err != nil {
			return err
		}

		source, err := withMetadata(s.FS, SourceMetadata{
			RepoURL: RepoURLForSource(ref.CanonicalSource),
			Ref:     snap.Ref.Full,
			TreeSHA: treeSHA,
			Path:    s.Path,
			Pinned:  ref.Ref,
		})
		if err != nil {
			return fmt.Errorf("%q のメタデータの注入: %w", s.Name, err)
		}

		replacements[i] = skillReplacement{Name: s.Name, Source: source, Style: ghStyle}
	}

	tx, err := stageSkillReplacements(layout, replacements)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tx.Cleanup()) }()

	return tx.Apply()
}

// ApplyCandidateUpdates は candidates のスキルをステージ済みの内容で置き換え、
// 新しいハッシュを lf に記録する。ロックファイルの書き込みに失敗したら
// ファイルシステムの変更をロールバックする
func ApplyCandidateUpdates(lf lock.File, candidates []UpdateCandidate, layout lock.Layout) (err error) {
	replacements := make([]skillReplacement, 0, len(candidates))
	for _, c := range candidates {
		replacements = append(replacements, skillReplacement{
			Name:   c.SkillName,
			Source: c.Upstream,
			Style:  vercelStyle,
		})

		entry := lf.Skills[c.SkillName]
		entry.ComputedHash = c.LatestHash
		entry.SkillPath = path.Join(c.Path, "SKILL.md")
		lf.Skills[c.SkillName] = entry
	}

	tx, err := stageSkillReplacements(layout, replacements)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tx.Cleanup()) }()

	if err := tx.Apply(); err != nil {
		return err
	}

	if err := lock.WriteFile(layout.LockPath(), lf); err != nil {
		return errors.Join(fmt.Errorf("更新後のロックファイルの書き込み: %w", err), tx.Rollback())
	}

	return nil
}

// ReplaceSkill はインストール済みのスキルのディレクトリを source でアトミックに置き換える。
// gh skill のインストールと同じ方式である
func ReplaceSkill(skillName string, source fs.FS, layout lock.Layout) (err error) {
	tx, err := stageSkillReplacements(layout, []skillReplacement{{
		Name:   skillName,
		Source: source,
		Style:  ghStyle,
	}})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tx.Cleanup()) }()

	return tx.Apply()
}

func stageSkillReplacements(layout lock.Layout, replacements []skillReplacement) (*replacementTxn, error) {
	if len(replacements) == 0 {
		return nil, errors.New("適用するスキルの変更がありません")
	}

	if err := os.MkdirAll(layout.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("インストール先のディレクトリ %q の作成: %w", layout.Dir, err)
	}

	tx := &replacementTxn{layout: layout}
	for _, replacement := range replacements {
		stagedDir, err := tempPath(layout.Dir, "."+replacement.Name+".staged-*")
		if err == nil {
			err = os.Mkdir(stagedDir, 0o755)
		}

		if err != nil {
			return nil, errors.Join(fmt.Errorf("%q のステージ用ディレクトリの作成: %w", replacement.Name, err), tx.Cleanup())
		}

		if err := copyDir(replacement.Source, stagedDir, replacement.Style); err != nil {
			return nil, errors.Join(fmt.Errorf("スキル %q のステージ: %w", replacement.Name, err), tx.Cleanup())
		}

		tx.staged = append(tx.staged, stagedSkillReplacement{
			Name:      replacement.Name,
			StagedDir: stagedDir,
		})
	}

	return tx, nil
}

func (tx *replacementTxn) Apply() error {
	for _, replacement := range tx.staged {
		backupDir, err := tx.swapIn(replacement)
		if err != nil {
			return errors.Join(err, tx.Rollback())
		}

		tx.applied = append(tx.applied, appliedSkillReplacement{
			Name:      replacement.Name,
			Dir:       tx.layout.SkillDir(replacement.Name),
			BackupDir: backupDir,
		})
	}

	return nil
}

func (tx *replacementTxn) Rollback() error {
	var errs []error

	for _, applied := range slices.Backward(tx.applied) {
		if err := os.RemoveAll(applied.Dir); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("適用済みのスキル %q の削除: %w", applied.Name, err))
			continue
		}

		if applied.BackupDir == "" {
			continue
		}

		if err := os.Rename(applied.BackupDir, applied.Dir); err != nil {
			errs = append(errs, fmt.Errorf("%q のバックアップの復元: %w", applied.Name, err))
		}
	}

	tx.applied = nil

	return errors.Join(errs...)
}

func (tx *replacementTxn) Cleanup() error {
	var errs []error

	for _, replacement := range tx.staged {
		if replacement.StagedDir == "" {
			continue
		}

		if err := os.RemoveAll(replacement.StagedDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("ステージ済みのスキル %q の後始末: %w", replacement.Name, err))
		}
	}

	for _, applied := range tx.applied {
		if applied.BackupDir == "" {
			continue
		}

		if err := os.RemoveAll(applied.BackupDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%q のバックアップの後始末: %w", applied.Name, err))
		}
	}

	return errors.Join(errs...)
}

func (tx *replacementTxn) swapIn(replacement stagedSkillReplacement) (string, error) {
	dst := tx.layout.SkillDir(replacement.Name)
	backupDir := ""

	if _, err := os.Stat(dst); err == nil {
		path, err := tempPath(tx.layout.Dir, "."+replacement.Name+".backup-*")
		if err != nil {
			return "", fmt.Errorf("%q のバックアップの準備: %w", replacement.Name, err)
		}

		if err := os.Rename(dst, path); err != nil {
			return "", fmt.Errorf("既存のスキル %q のバックアップ: %w", replacement.Name, err)
		}

		backupDir = path
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("既存のスキル %q の stat: %w", replacement.Name, err)
	}

	if err := os.Rename(replacement.StagedDir, dst); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, dst)
		}

		return "", fmt.Errorf("ステージ済みのスキル %q の有効化: %w", replacement.Name, err)
	}

	return backupDir, nil
}

func tempPath(parentDir, pattern string) (string, error) {
	path, err := os.MkdirTemp(parentDir, pattern)
	if err != nil {
		return "", err
	}

	if err := os.Remove(path); err != nil {
		return "", err
	}

	return path, nil
}
