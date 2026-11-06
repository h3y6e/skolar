package skill_test

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

func TestResolveUpdateCandidates(t *testing.T) {
	t.Parallel()

	t.Run("ハッシュが一致するとき、状態は最新になる", func(t *testing.T) {
		t.Parallel()

		lockEntries := map[string]lock.Entry{
			"my-skill": {
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: hashOf("abc123"),
			},
		}

		upstream := []skill.DiscoveredSkill{
			fakeSkill("my-skill", "", "abc123"),
		}

		candidates := skill.ResolveUpdateCandidates(lockEntries, upstream)

		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}

		c := candidates[0]
		if c.SkillName != "my-skill" {
			t.Errorf("SkillName = %q, want %q", c.SkillName, "my-skill")
		}

		if c.Status != skill.StatusUpToDate {
			t.Errorf("Status = %q, want %q", c.Status, skill.StatusUpToDate)
		}

		if c.CurrentHash != hashOf("abc123") {
			t.Errorf("CurrentHash = %q, want %q", c.CurrentHash, hashOf("abc123"))
		}

		if c.LatestHash != hashOf("abc123") {
			t.Errorf("LatestHash = %q, want %q", c.LatestHash, hashOf("abc123"))
		}
	})

	t.Run("ハッシュが異なるとき、状態は更新ありになる", func(t *testing.T) {
		t.Parallel()

		lockEntries := map[string]lock.Entry{
			"my-skill": {
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: hashOf("old-hash"),
			},
		}

		upstream := []skill.DiscoveredSkill{
			fakeSkill("my-skill", "", "new-hash"),
		}

		candidates := skill.ResolveUpdateCandidates(lockEntries, upstream)

		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}

		c := candidates[0]
		if c.Status != skill.StatusUpdateAvailable {
			t.Errorf("Status = %q, want %q", c.Status, skill.StatusUpdateAvailable)
		}

		if c.CurrentHash != hashOf("old-hash") {
			t.Errorf("CurrentHash = %q, want %q", c.CurrentHash, hashOf("old-hash"))
		}

		if c.LatestHash != hashOf("new-hash") {
			t.Errorf("LatestHash = %q, want %q", c.LatestHash, hashOf("new-hash"))
		}

		if c.Upstream == nil {
			t.Error("Upstream = nil, want the upstream skill")
		}
	})

	t.Run("複数のスキルがあるとき、すべてが解決される", func(t *testing.T) {
		t.Parallel()

		lockEntries := map[string]lock.Entry{
			"skill-a": {
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: hashOf("hash-a"),
			},
			"skill-b": {
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: hashOf("hash-b-old"),
			},
		}

		upstream := []skill.DiscoveredSkill{
			fakeSkill("skill-a", "", "hash-a"),
			fakeSkill("skill-b", "", "hash-b-new"),
		}

		candidates := skill.ResolveUpdateCandidates(lockEntries, upstream)

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		byName := map[string]skill.UpdateCandidate{}
		for _, c := range candidates {
			byName[c.SkillName] = c
		}

		if byName["skill-a"].Status != skill.StatusUpToDate {
			t.Errorf("skill-a Status = %q, want %q", byName["skill-a"].Status, skill.StatusUpToDate)
		}

		if byName["skill-b"].Status != skill.StatusUpdateAvailable {
			t.Errorf("skill-b Status = %q, want %q", byName["skill-b"].Status, skill.StatusUpdateAvailable)
		}
	})

	t.Run("候補を解決したとき、名前順に並ぶ", func(t *testing.T) {
		t.Parallel()

		lockEntries := map[string]lock.Entry{
			"zeta":  {Source: "o/r", SourceType: "github", ComputedHash: hashOf("h1")},
			"alpha": {Source: "o/r", SourceType: "github", ComputedHash: hashOf("h2")},
		}

		upstream := []skill.DiscoveredSkill{
			fakeSkill("zeta", "", "h1"),
			fakeSkill("alpha", "", "h2"),
		}

		candidates := skill.ResolveUpdateCandidates(lockEntries, upstream)

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		if candidates[0].SkillName != "alpha" {
			t.Errorf("first candidate = %q, want %q", candidates[0].SkillName, "alpha")
		}

		if candidates[1].SkillName != "zeta" {
			t.Errorf("second candidate = %q, want %q", candidates[1].SkillName, "zeta")
		}
	})
}

func TestFilterCandidates(t *testing.T) {
	t.Parallel()

	candidates := []skill.UpdateCandidate{
		{SkillName: "a", Status: skill.StatusUpToDate},
		{SkillName: "b", Status: skill.StatusUpdateAvailable},
		{SkillName: "c", Status: skill.StatusCheckFailed},
	}

	t.Run("filter が nil のとき、全件が返る", func(t *testing.T) {
		t.Parallel()

		result := skill.FilterCandidates(candidates, nil)
		if len(result) != 3 {
			t.Fatalf("expected 3, got %d", len(result))
		}
	})

	t.Run("filter が空のとき、全件が返る", func(t *testing.T) {
		t.Parallel()

		result := skill.FilterCandidates(candidates, []string{})
		if len(result) != 3 {
			t.Fatalf("expected 3, got %d", len(result))
		}
	})

	t.Run("スキルを指定したとき、そのスキルだけに絞られる", func(t *testing.T) {
		t.Parallel()

		result := skill.FilterCandidates(candidates, []string{"b"})
		if len(result) != 1 {
			t.Fatalf("expected 1, got %d", len(result))
		}

		if result[0].SkillName != "b" {
			t.Errorf("SkillName = %q, want %q", result[0].SkillName, "b")
		}
	})

	t.Run("複数のスキルを指定したとき、それらだけに絞られる", func(t *testing.T) {
		t.Parallel()

		result := skill.FilterCandidates(candidates, []string{"a", "c"})
		if len(result) != 2 {
			t.Fatalf("expected 2, got %d", len(result))
		}
	})
}

func collectLocked(entries map[string]lock.Entry, cloneFn skill.CloneFunc) ([]skill.UpdateCandidate, []skill.SkippedEntry) {
	return skill.CollectUpdateCandidates(entries, nil, cloneFn, skill.UpstreamFuncs{}, func(int, int) {})
}

func TestCollectUpdateCandidates(t *testing.T) {
	t.Parallel()

	t.Run("エントリがあるとき、ソースごとにまとめて候補が解決される", func(t *testing.T) {
		t.Parallel()

		srcA := t.TempDir()
		srcB := t.TempDir()

		testutil.WriteFiles(t, srcA, map[string]string{
			"skills/skill-x/SKILL.md": "# Skill X updated\n",
		})
		testutil.WriteFiles(t, srcB, map[string]string{
			"skills/skill-y/SKILL.md": "# Skill Y same\n",
		})

		discoveredA, err := skill.DiscoverSkills(os.DirFS(srcA))
		if err != nil {
			t.Fatal(err)
		}

		discoveredB, err := skill.DiscoverSkills(os.DirFS(srcB))
		if err != nil {
			t.Fatal(err)
		}

		entries := map[string]lock.Entry{
			"skill-x": {
				Source:       "h3y6e/skills-a",
				SourceType:   "github",
				ComputedHash: hashOf("old-hash-x"),
			},
			"skill-y": {
				Source:       "h3y6e/skills-b",
				SourceType:   "github",
				ComputedHash: mustHash(t, discoveredB[0]),
			},
		}

		cloneDirs := map[string]string{
			"h3y6e/skills-a": srcA,
			"h3y6e/skills-b": srcB,
		}
		cloneFn := func(source string) (fs.FS, error) {
			dir, ok := cloneDirs[source]
			if !ok {
				t.Fatalf("unexpected clone for source %q", source)
			}

			return os.DirFS(dir), nil
		}

		candidates, _ := collectLocked(entries, cloneFn)

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		byName := map[string]skill.UpdateCandidate{}
		for _, c := range candidates {
			byName[c.SkillName] = c
		}

		if byName["skill-x"].Status != skill.StatusUpdateAvailable {
			t.Errorf("skill-x Status = %q, want %q", byName["skill-x"].Status, skill.StatusUpdateAvailable)
		}

		if byName["skill-x"].LatestHash != mustHash(t, discoveredA[0]) {
			t.Errorf("skill-x LatestHash = %q, want %q", byName["skill-x"].LatestHash, mustHash(t, discoveredA[0]))
		}

		if byName["skill-y"].Status != skill.StatusUpToDate {
			t.Errorf("skill-y Status = %q, want %q", byName["skill-y"].Status, skill.StatusUpToDate)
		}
	})

	t.Run("エントリがあるとき、ソースと ref ごとにまとめられる", func(t *testing.T) {
		t.Parallel()

		srcMain := t.TempDir()
		srcFeature := t.TempDir()

		testutil.WriteFiles(t, srcMain, map[string]string{
			"skills/shared-main/SKILL.md": "# Main\n",
		})
		testutil.WriteFiles(t, srcFeature, map[string]string{
			"skills/shared-feature/SKILL.md": "# Feature\n",
		})

		discoveredMain, err := skill.DiscoverSkills(os.DirFS(srcMain))
		if err != nil {
			t.Fatal(err)
		}

		discoveredFeature, err := skill.DiscoverSkills(os.DirFS(srcFeature))
		if err != nil {
			t.Fatal(err)
		}

		entries := map[string]lock.Entry{
			"shared-main": {
				Source:       "h3y6e/spec-skills",
				Ref:          "main",
				SourceType:   "github",
				ComputedHash: hashOf("stale-main"),
			},
			"shared-feature": {
				Source:       "h3y6e/spec-skills",
				Ref:          "feature/install",
				SourceType:   "github",
				ComputedHash: hashOf("stale-feature"),
			},
		}

		cloneDirs := map[string]string{
			"h3y6e/spec-skills#main":            srcMain,
			"h3y6e/spec-skills#feature/install": srcFeature,
		}
		cloneFn := func(source string) (fs.FS, error) {
			dir, ok := cloneDirs[source]
			if !ok {
				t.Fatalf("unexpected clone for source %q", source)
			}

			return os.DirFS(dir), nil
		}

		candidates, _ := collectLocked(entries, cloneFn)

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		byName := map[string]skill.UpdateCandidate{}
		for _, candidate := range candidates {
			byName[candidate.SkillName] = candidate
		}

		if byName["shared-main"].Ref != "main" {
			t.Errorf("shared-main Ref = %q, want %q", byName["shared-main"].Ref, "main")
		}

		if byName["shared-main"].LatestHash != mustHash(t, discoveredMain[0]) {
			t.Errorf("shared-main LatestHash = %q, want %q", byName["shared-main"].LatestHash, mustHash(t, discoveredMain[0]))
		}

		if byName["shared-feature"].Ref != "feature/install" {
			t.Errorf("shared-feature Ref = %q, want %q", byName["shared-feature"].Ref, "feature/install")
		}

		if byName["shared-feature"].LatestHash != mustHash(t, discoveredFeature[0]) {
			t.Errorf("shared-feature LatestHash = %q, want %q", byName["shared-feature"].LatestHash, mustHash(t, discoveredFeature[0]))
		}
	})

	t.Run("複数のソースがあるとき、全体で名前順の候補が返る", func(t *testing.T) {
		t.Parallel()

		srcDir := t.TempDir()
		testutil.WriteFiles(t, srcDir, map[string]string{
			"skills/zeta/SKILL.md":  "# Zeta\n",
			"skills/alpha/SKILL.md": "# Alpha\n",
		})

		discovered, err := skill.DiscoverSkills(os.DirFS(srcDir))
		if err != nil {
			t.Fatal(err)
		}

		hashByName := map[string]string{}
		for _, d := range discovered {
			hashByName[d.Name] = mustHash(t, d)
		}

		entries := map[string]lock.Entry{
			"zeta":  {Source: "o/r", SourceType: "github", ComputedHash: hashByName["zeta"]},
			"alpha": {Source: "o/r", SourceType: "github", ComputedHash: hashByName["alpha"]},
		}

		cloneFn := func(_ string) (fs.FS, error) {
			return os.DirFS(srcDir), nil
		}

		candidates, _ := collectLocked(entries, cloneFn)

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		if candidates[0].SkillName != "alpha" {
			t.Errorf("first = %q, want 'alpha'", candidates[0].SkillName)
		}

		if candidates[1].SkillName != "zeta" {
			t.Errorf("second = %q, want 'zeta'", candidates[1].SkillName)
		}
	})

	t.Run("複数のソースがあるとき、並行に処理される", func(t *testing.T) {
		t.Parallel()

		srcA := t.TempDir()
		srcB := t.TempDir()

		testutil.WriteFiles(t, srcA, map[string]string{
			"skills/alpha/SKILL.md": "# Alpha\n",
		})
		testutil.WriteFiles(t, srcB, map[string]string{
			"skills/beta/SKILL.md": "# Beta\n",
		})

		entries := map[string]lock.Entry{
			"alpha": {Source: "h3y6e/skills-a", SourceType: "github", ComputedHash: hashOf("stale-a")},
			"beta":  {Source: "h3y6e/skills-b", SourceType: "github", ComputedHash: hashOf("stale-b")},
		}

		cloneDirs := map[string]string{
			"h3y6e/skills-a": srcA,
			"h3y6e/skills-b": srcB,
		}
		started := make(chan string, len(cloneDirs))
		release := make(chan struct{})

		cloneFn := func(source string) (fs.FS, error) {
			dir, ok := cloneDirs[source]
			if !ok {
				t.Fatalf("unexpected clone for source %q", source)
			}

			started <- source

			<-release

			return os.DirFS(dir), nil
		}

		done := make(chan struct{})

		var candidates []skill.UpdateCandidate
		go func() {
			candidates, _ = collectLocked(entries, cloneFn)

			close(done)
		}()

		seen := map[string]bool{}
		for len(seen) < len(cloneDirs) {
			select {
			case source := <-started:
				seen[source] = true
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("expected %d concurrent clone starts, got %d", len(cloneDirs), len(seen))
			}
		}

		close(release)

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("CollectUpdateCandidates() did not finish")
		}

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}
	})

	t.Run("記録された上流の SKILL.md が不正なとき、削除とは報告せず確認が失敗する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		src := t.TempDir()
		testutil.WriteFiles(t, src, map[string]string{"skills/a/SKILL.md": "---\nname: a\ncompatibility: MCP (tools: x)\n---\n"})

		entries := map[string]lock.Entry{"a": {Source: "o/r", SourceType: "github", SkillPath: "skills/a/SKILL.md", ComputedHash: hashOf("h")}}

		// Act
		candidates, _ := collectLocked(entries, func(string) (fs.FS, error) { return os.DirFS(src), nil })

		// Assert
		if len(candidates) != 1 || candidates[0].Status != skill.StatusCheckFailed {
			t.Errorf("candidates = %+v, want a check-failed", candidates)
		}
	})

	t.Run("ロックファイルと gh skill のインストールが同じソースを共有するとき、どちらにも未インストールのスキルは 1 度だけ列挙される", func(t *testing.T) {
		t.Parallel()
		// Arrange
		src := t.TempDir()
		testutil.WriteFiles(t, src, map[string]string{
			"skills/a/SKILL.md": "# A\n",
			"skills/b/SKILL.md": "# B\n",
			"skills/c/SKILL.md": "# C\n",
		})

		entries := map[string]lock.Entry{"a": {Source: "o/r", SourceType: "github", SkillPath: "skills/a/SKILL.md"}}
		installed := []skill.InstalledSkill{{Name: "b", Meta: skill.SourceMetadata{RepoURL: "https://github.com/o/r", TreeSHA: "t", Path: "skills/b"}}}
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				return snapshotOf(t, src, "t", nil), nil
			},
		}

		// Act
		candidates, _ := skill.CollectUpdateCandidates(entries, installed, func(string) (fs.FS, error) { return os.DirFS(src), nil }, fns, func(int, int) {})

		// Assert
		var news []string

		for _, c := range candidates {
			if c.Status == skill.StatusNew {
				news = append(news, c.SkillName)
			}
		}

		if !slices.Equal(news, []string{"c"}) {
			t.Errorf("new skills = %v, want [c]", news)
		}
	})

	t.Run("1 つのソースを clone できないとき、そのスキルだけが確認に失敗する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		good := t.TempDir()
		testutil.WriteFiles(t, good, map[string]string{"skills/b/SKILL.md": "# B\n"})

		entries := map[string]lock.Entry{
			"a": {Source: "bad/source", SourceType: "github", ComputedHash: hashOf("h")},
			"b": {Source: "good/source", SourceType: "github", ComputedHash: hashOf("h")},
		}
		cloneFn := func(source string) (fs.FS, error) {
			if source == "bad/source" {
				return nil, errors.New("clone failed")
			}

			return os.DirFS(good), nil
		}

		// Act
		candidates, _ := collectLocked(entries, cloneFn)

		// Assert
		got := []skill.Status{candidates[0].Status, candidates[1].Status}
		if !slices.Equal(got, []skill.Status{skill.StatusCheckFailed, skill.StatusUpdateAvailable}) {
			t.Errorf("statuses = %v, want check-failed for a and update-available for b", got)
		}
	})

	t.Run("ロックファイルが空のとき、候補は空になる", func(t *testing.T) {
		t.Parallel()

		cloneFn := func(_ string) (fs.FS, error) {
			t.Fatal("should not be called")
			return nil, nil
		}

		candidates, _ := collectLocked(map[string]lock.Entry{}, cloneFn)

		if len(candidates) != 0 {
			t.Errorf("expected 0 candidates, got %d", len(candidates))
		}
	})

	t.Run("未対応のソース種別があるとき、スキップされる", func(t *testing.T) {
		t.Parallel()

		srcDir := t.TempDir()
		localSrcDir := t.TempDir()
		testutil.WriteFiles(t, srcDir, map[string]string{
			"skills/supported-skill/SKILL.md": "# Supported\n",
		})
		testutil.WriteFiles(t, localSrcDir, map[string]string{
			"skills/local-skill/SKILL.md": "# Local\n",
		})

		discovered, err := skill.DiscoverSkills(os.DirFS(srcDir))
		if err != nil {
			t.Fatal(err)
		}

		localDiscovered, err := skill.DiscoverSkills(os.DirFS(localSrcDir))
		if err != nil {
			t.Fatal(err)
		}

		entries := map[string]lock.Entry{
			"supported-skill": {
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: mustHash(t, discovered[0]),
			},
			"local-skill": {
				Source:       localSrcDir,
				SourceType:   "local",
				ComputedHash: mustHash(t, localDiscovered[0]),
			},
			"wellknown-skill": {
				Source:       "mintlify/docs",
				SourceType:   "well-known",
				ComputedHash: hashOf("wkhash"),
			},
			"npm-skill": {
				Source:       "my-npm-pkg",
				SourceType:   "node_modules",
				ComputedHash: hashOf("npmhash"),
			},
		}

		cloneFn := func(source string) (fs.FS, error) {
			if source == "h3y6e/spec-skills" {
				return os.DirFS(srcDir), nil
			}

			if source == localSrcDir {
				return os.DirFS(localSrcDir), nil
			}

			t.Fatalf("unexpected clone for source %q", source)

			return nil, nil
		}

		candidates, skipped := collectLocked(entries, cloneFn)

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		candidateNames := []string{candidates[0].SkillName, candidates[1].SkillName}
		if !slices.Equal(candidateNames, []string{"local-skill", "supported-skill"}) {
			t.Errorf("candidate names = %v, want %v", candidateNames, []string{"local-skill", "supported-skill"})
		}

		if len(skipped) != 2 {
			t.Fatalf("expected 2 skipped, got %d", len(skipped))
		}

		skippedByName := map[string]skill.SkippedEntry{}
		for _, s := range skipped {
			skippedByName[s.SkillName] = s
		}

		wantSkipped := map[string]lock.SourceType{
			"wellknown-skill": "well-known",
			"npm-skill":       "node_modules",
		}
		for name, wantType := range wantSkipped {
			got, ok := skippedByName[name]
			if !ok {
				t.Errorf("expected %q in skipped entries", name)
				continue
			}

			if got.SourceType != wantType {
				t.Errorf("skipped %q SourceType = %q, want %q", name, got.SourceType, wantType)
			}
		}
	})
}

func TestResolveUpdateCandidatesBySkillPath(t *testing.T) {
	t.Parallel()

	at := func(name, path, hash string) skill.DiscoveredSkill {
		s := fakeSkill(name, path, hash)
		s.Default = true

		return s
	}

	type want struct {
		name   string
		status skill.Status
		path   string
	}

	for _, tc := range []struct {
		name     string
		entry    lock.Entry
		upstream []skill.DiscoveredSkill
		want     []want
	}{
		{
			"エントリの skillPath が上流にあるとき、別のディレクトリが同名でもそのパスのスキルと比較される",
			lock.Entry{SkillPath: ".claude/skills/dup/SKILL.md", ComputedHash: hashOf("b")},
			[]skill.DiscoveredSkill{at("dup", "skills/dup", "a"), at("dup", ".claude/skills/dup", "b")},
			[]want{{"dup", skill.StatusUpToDate, ".claude/skills/dup"}},
		},
		{
			"エントリの skillPath がなくなり同名の上流スキルが 1 つあるとき、スキルはそこへ移動したと判定される",
			lock.Entry{SkillPath: "old/dup/SKILL.md", ComputedHash: hashOf("a")},
			[]skill.DiscoveredSkill{at("dup", "skills/dup", "a")},
			[]want{{"dup", skill.StatusMoved, "skills/dup"}},
		},
		{
			"エントリの skillPath がなくなり同名の上流スキルもないとき、スキルは上流で削除されたと判定される",
			lock.Entry{SkillPath: "skills/dup/SKILL.md", ComputedHash: hashOf("a")},
			[]skill.DiscoveredSkill{at("other", "skills/other", "a")},
			[]want{{"dup", skill.StatusRemoved, ""}, {"other", skill.StatusNew, "skills/other"}},
		},
		{
			"インストール済みスキルのパスが別の上流スキルの名前と同じとき、その上流スキルは新規のままになる",
			lock.Entry{SkillPath: "other/SKILL.md", ComputedHash: hashOf("a")},
			[]skill.DiscoveredSkill{at("dup", "other", "a"), at("other", "skills/other", "b")},
			[]want{{"dup", skill.StatusUpToDate, "other"}, {"other", skill.StatusNew, "skills/other"}},
		},
		{
			"エントリに skillPath がなく同名の上流スキルが 2 つあるとき、曖昧として確認が失敗する",
			lock.Entry{ComputedHash: hashOf("a")},
			[]skill.DiscoveredSkill{at("dup", "skills/dup", "a"), at("dup", ".claude/skills/dup", "b")},
			[]want{{"dup", skill.StatusCheckFailed, ""}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Act
			candidates := skill.ResolveUpdateCandidates(map[string]lock.Entry{"dup": tc.entry}, tc.upstream)

			// Assert
			got := make([]want, len(candidates))
			for i, c := range candidates {
				got[i] = want{c.SkillName, c.Status, c.Path}
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("candidates = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func fakeSkill(name, path, content string) skill.DiscoveredSkill {
	return skill.DiscoveredSkill{Name: name, Path: path, FS: fstest.MapFS{"SKILL.md": {Data: []byte(content)}}}
}

func hashOf(content string) string {
	hash, err := skill.ComputeHash(fstest.MapFS{"SKILL.md": {Data: []byte(content)}})
	if err != nil {
		panic(err)
	}

	return hash
}

func mustHash(t *testing.T, s skill.DiscoveredSkill) string {
	t.Helper()

	hash, err := s.Hash()
	if err != nil {
		t.Fatal(err)
	}

	return hash
}
