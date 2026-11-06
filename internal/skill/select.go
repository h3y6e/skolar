package skill

import (
	"fmt"
	"path"
	"slices"
)

// Selected は name が patterns のどれかに一致するかを返す。patterns は "spec-*" のような
// シェルの glob で、path.Match が受け付けるものである
func Selected(patterns []string, name string) bool {
	return slices.ContainsFunc(patterns, func(p string) bool {
		ok, _ := path.Match(p, name)
		return ok
	})
}

// CheckPatterns は最初に見つかった不正なパターンを報告する
func CheckPatterns(patterns []string) error {
	for _, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("スキルのパターン %q: %w", p, err)
		}
	}

	return nil
}
