package skill

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Stat は更新の適用で追加・削除された行数と変更されたファイル数を数える。
// モードの変更は数えない
type Stat struct {
	Added   int
	Deleted int
	Files   int
}

// DiffStat は c の適用が dir のインストール済みスキルに加える変更を数える。
// 行数は git diff --minimal --numstat と同じ方式で数え、バイナリファイルは 0 行とする
func DiffStat(c UpdateCandidate, dir string) (Stat, error) {
	s := vercelStyle
	if c.Foreign {
		s = ghStyle
	}

	want, got, err := readBoth(c.Upstream, os.DirFS(dir), s)
	if err != nil {
		return Stat{}, err
	}

	if c.Foreign {
		meta, err := ParseSourceMetadata(string(want["SKILL.md"]))
		if err != nil {
			return Stat{}, err
		}

		if old, ok := got["SKILL.md"]; ok {
			injected, err := InjectSourceMetadata(string(old), meta)
			if err != nil {
				return Stat{}, err
			}

			got["SKILL.md"] = []byte(injected)
		}
	}

	changed := changedFiles(want, got)

	var stat Stat

	for _, p := range changed {
		added, deleted := lineDiff(got[p], want[p])
		stat.Added += added
		stat.Deleted += deleted
	}

	links, err := linkPaths(dir)
	stat.Files = len(changed)

	for _, l := range links {
		if !slices.Contains(changed, l) {
			stat.Files++
		}
	}

	return stat, err
}

func linkPaths(dir string) ([]string, error) {
	var links []string

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			rel, _ := filepath.Rel(dir, p)
			links = append(links, filepath.ToSlash(rel))
		}

		return err
	})

	return links, err
}

func lineDiff(a, b []byte) (added, deleted int) {
	if isBinary(a) || isBinary(b) {
		return 0, 0
	}

	from, to := splitLines(a), splitLines(b)
	common := lcsLength(internLines(from, to))

	return len(to) - common, len(from) - common
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

func splitLines(data []byte) []string {
	var lines []string

	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n') + 1
		if i == 0 {
			i = len(data)
		}

		lines = append(lines, string(data[:i]))
		data = data[i:]
	}

	return lines
}

func internLines(a, b []string) (x, y []int) {
	ids := map[string]int{}
	sides := map[string]int{}

	for _, l := range a {
		sides[l] |= 1
	}

	for _, l := range b {
		sides[l] |= 2
	}

	number := func(lines []string) []int {
		var out []int

		for _, l := range lines {
			if sides[l] != 3 {
				continue
			}

			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}

			out = append(out, id)
		}

		return out
	}

	return number(a), number(b)
}

func lcsLength(x, y []int) int {
	n, m := len(x), len(y)
	offset := n + m + 1
	v := make([]int, 2*offset+1)

	for d := 0; d <= n+m; d++ {
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || k != d && v[offset+k-1] < v[offset+k+1] {
				i = v[offset+k+1]
			} else {
				i = v[offset+k-1] + 1
			}

			j := i - k
			for i < n && j < m && x[i] == y[j] {
				i++
				j++
			}

			v[offset+k] = i

			if i >= n && j >= m {
				return (n + m - d) / 2
			}
		}
	}

	return 0
}
