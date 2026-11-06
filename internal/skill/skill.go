// Package skill はエージェントスキルの探索、インストール、更新確認、検証を行う
package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"slices"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// ComputeHash は fsys のスキルについて vercel-labs/skills が記録する computedHash を返す。
// 通常ファイルのパスと内容を locale 順に並べた SHA-256 で、.git と node_modules は除く
func ComputeHash(fsys fs.FS) (string, error) {
	var files []string

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && p != "." && (d.Name() == ".git" || d.Name() == "node_modules") {
			return fs.SkipDir
		}

		if d.Type().IsRegular() {
			files = append(files, p)
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("ハッシュの計算: %w", err)
	}

	slices.SortFunc(files, collate.New(language.Und).CompareString)

	h := sha256.New()

	for _, p := range files {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return "", fmt.Errorf("ハッシュの計算: %w", err)
		}

		_, _ = io.WriteString(h, p)
		_, _ = h.Write(data)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
