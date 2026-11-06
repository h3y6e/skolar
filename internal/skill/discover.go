package skill

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// DiscoveredSkill は上流のリポジトリで見つかったスキルを表す
type DiscoveredSkill struct {
	Name string
	Path string // スキルのリポジトリからの相対ディレクトリ。ルートは "."
	FS   fs.FS
	// Default は vercel-labs/skills または gh skill が探す場所を指定されなくても
	// そのスキルを見つけるかを表す
	Default bool
}

// SkillMDPath はスキルの SKILL.md のリポジトリからの相対パスを返す。
// vercel-labs/skills が skillPath に記録するものと同じである
func (s DiscoveredSkill) SkillMDPath() string {
	return path.Join(s.Path, "SKILL.md")
}

// Hash はスキルの computedHash を返す
func (s DiscoveredSkill) Hash() (string, error) {
	return ComputeHash(s.FS)
}

func subFS(fsys fs.FS, dir string) fs.FS {
	if dir == "." {
		return fsys
	}

	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}

	return sub
}

// lstat は fsys の name の種別を末尾のリンクをたどらずに返す。
// os.DirFS の fs.Stat はたどってしまう
func lstat(fsys fs.FS, name string) (fs.FileMode, bool) {
	if name == "." {
		info, err := fs.Stat(fsys, ".")
		if err != nil {
			return 0, false
		}

		return info.Mode().Type(), true
	}

	entries, err := fs.ReadDir(fsys, path.Dir(name))
	if err != nil {
		return 0, false
	}

	i := slices.IndexFunc(entries, func(e fs.DirEntry) bool { return e.Name() == path.Base(name) })
	if i < 0 {
		return 0, false
	}

	return entries[i].Type(), true
}

func isRegular(fsys fs.FS, name string) bool {
	mode, ok := lstat(fsys, name)
	return ok && mode.IsRegular()
}

const skillsSegment = "skills"

// skipDirs は vercel-labs/skills と同じく探索しないディレクトリである
var skipDirs = []string{".git", "node_modules", "dist", "build", "__pycache__"}

// vercelContainers は vercel-labs/skills が 3 階層の深さまで探索するディレクトリで、
// 見つからなければリポジトリ全体の探索に移る
var vercelContainers = []string{
	"skills", "skills/.curated", "skills/.experimental", "skills/.system",
	".agents/skills", ".claude/skills", ".cline/skills", ".codebuddy/skills", ".codex/skills",
	".commandcode/skills", ".continue/skills", ".factory/skills", ".github/skills", ".goose/skills",
	".grok/skills", ".iflow/skills", ".junie/skills", ".kilo/skills", ".kilocode/skills",
	".kimchi/skills", ".kiro/skills", ".minimax/skills", ".mux/skills", ".neovate/skills",
	".opencode/skills", ".openhands/skills", ".pi/skills", ".posit/assistant/skills", ".qoder/skills",
	".roo/skills", ".trae/skills", ".windsurf/skills", ".zcode/skills", ".zencoder/skills",
}

type skillDir struct {
	path        string // リポジトリからの相対パス。ルートは "."
	name        string
	vercelValid bool
}

// DiscoverSkills はリポジトリ内の vercel-labs/skills (--full-depth 付き) または
// gh skill (隠しディレクトリ付き) がインストールできるスキルをすべて
// vercel-labs/skills の探索順で見つける。名前はディレクトリをまたいで重複し得る
func DiscoverSkills(repo fs.FS) ([]DiscoveredSkill, error) {
	all, err := findSkillDirs(repo)
	if err != nil {
		return nil, err
	}

	vercel := vercelDefaults(repo, all)

	defaults := map[string]bool{}
	for _, p := range vercel {
		defaults[p] = true
	}

	for p := range all {
		if ghDefault(p) {
			defaults[p] = true
		}
	}

	var skills []DiscoveredSkill
	for _, p := range slices.Compact(slices.Concat(vercel, slices.Sorted(maps.Keys(all)))) {
		if d := all[p]; d.name != "" && !slices.ContainsFunc(skills, func(s DiscoveredSkill) bool { return s.Path == p }) {
			skills = append(skills, DiscoveredSkill{Name: d.name, Path: p, FS: subFS(repo, p), Default: defaults[p]})
		}
	}

	return skills, nil
}

func findSkillDirs(repo fs.FS) (map[string]skillDir, error) {
	dirs := map[string]skillDir{}
	err := fs.WalkDir(repo, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}

		if isRegular(repo, path.Join(p, "SKILL.md")) {
			data, err := fs.ReadFile(repo, path.Join(p, "SKILL.md"))
			if err != nil {
				return err
			}

			if dir, ok := readSkillDir(data, p); ok {
				dirs[dir.path] = dir
			}
		}

		if p != "." && slices.Contains(skipDirs, d.Name()) {
			return fs.SkipDir
		}

		return nil
	})

	return dirs, err
}

func readSkillDir(data []byte, rel string) (skillDir, bool) {
	raw, _, err := parseFrontmatter(string(data))
	if err != nil {
		return skillDir{}, false
	}

	name, _ := raw["name"].(string)
	if name != "" && (name == "." || !filepath.IsLocal(name) || filepath.Base(name) != name) {
		return skillDir{}, false
	}

	description, _ := raw["description"].(string)
	metadata, _ := raw["metadata"].(map[string]any)
	internal := metadata["internal"] == true && !slices.Contains([]string{"1", "true"}, os.Getenv("INSTALL_INTERNAL_SKILLS"))

	d := skillDir{path: rel, name: name, vercelValid: name != "" && description != "" && !internal}
	if d.name == "" && rel != "." {
		d.name = path.Base(rel)
	}

	return d, true
}

// vercelDefaults は vercel-labs/skills が --full-depth なしでインストールする
// スキルのディレクトリを探索順に返す
func vercelDefaults(repo fs.FS, all map[string]skillDir) []string {
	if root, ok := all["."]; ok && root.vercelValid {
		return []string{"."}
	}

	search := &vercelSearch{repo: repo, all: all}
	search.walk(".", 1, 1)

	for _, c := range vercelContainers {
		search.walk(c, 1, 3)
	}

	for _, p := range pluginSkillDirs(repo) {
		search.walk(p, 1, 1)
	}

	if len(search.found) > 0 {
		return search.found
	}

	var valid []string

	for p, d := range all {
		if d.vercelValid {
			valid = append(valid, p)
		}
	}

	slices.Sort(valid)

	return valid
}

type vercelSearch struct {
	repo  fs.FS
	all   map[string]skillDir
	found []string
}

func (s *vercelSearch) walk(dir string, depth, maxDepth int) {
	entries, _ := fs.ReadDir(s.repo, dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		child := path.Join(dir, e.Name())
		if d, ok := s.all[child]; ok {
			if d.vercelValid && !slices.Contains(s.found, child) {
				s.found = append(s.found, child)
			}

			continue
		}

		if depth < maxDepth && !slices.Contains(skipDirs, e.Name()) {
			s.walk(child, depth+1, maxDepth)
		}
	}
}

// pluginSkillDirs は Claude プラグインのマニフェストがスキルの置き場として宣言する
// ディレクトリを vercel-labs/skills と同じ読み方で返す
func pluginSkillDirs(repo fs.FS) []string {
	var dirs []string

	add := func(base string, skills []string) {
		for _, s := range skills {
			if strings.HasPrefix(s, "./") {
				dirs = append(dirs, path.Dir(path.Join(base, s)))
			}
		}

		dirs = append(dirs, path.Join(base, skillsSegment))
	}

	var market struct {
		Metadata struct {
			PluginRoot string `json:"pluginRoot"`
		} `json:"metadata"`
		Plugins []struct {
			Source any      `json:"source"`
			Skills []string `json:"skills"`
		} `json:"plugins"`
	}
	if data, err := fs.ReadFile(repo, ".claude-plugin/marketplace.json"); err == nil && json.Unmarshal(data, &market) == nil {
		for _, p := range market.Plugins {
			source, ok := p.Source.(string)
			if p.Source != nil && (!ok || !strings.HasPrefix(source, "./")) {
				continue
			}

			add(path.Join(market.Metadata.PluginRoot, source), p.Skills)
		}
	}

	var plugin struct {
		Skills []string `json:"skills"`
	}
	if data, err := fs.ReadFile(repo, ".claude-plugin/plugin.json"); err == nil && json.Unmarshal(data, &plugin) == nil {
		add(".", plugin.Skills)
	}

	return dirs
}

// ghDefault は gh skill install がリポジトリからの相対パス p のスキルを
// --allow-hidden-dirs なしで探索するかを返す
func ghDefault(p string) bool {
	if p == "." {
		return false
	}

	segments := strings.Split(p, "/")
	for _, s := range segments {
		if strings.HasPrefix(s, ".") {
			return false
		}
	}

	n := len(segments)

	name := segments[n-1]
	switch {
	case n == 1:
		return name != skillsSegment && name != "plugins"
	case segments[n-2] == skillsSegment:
		return true
	case n >= 3 && segments[n-3] == skillsSegment:
		return true
	}

	return false
}
