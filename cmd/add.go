package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/spf13/cobra"
)

type addOptions struct {
	dir    string
	skills []string
	dryRun bool
	yes    bool
}

func newAddCmd() *cobra.Command {
	var opts addOptions

	c := &cobra.Command{
		Use:   "add <source>",
		Short: "ソースリポジトリからスキルを追加する (上書き時は破壊的)",
		Long: `ソースリポジトリからスキルを追加する (上書き時は破壊的)

--skill を指定しない場合はソースの既定のスキルをインストールする。--skill を指定すると SKILL.md を持つ任意のディレクトリを対象にできる`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd, args[0], opts)
		},
	}

	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "インストールされるスキルを表示するだけでインストールしない")
	c.Flags().BoolVarP(&opts.yes, "yes", "y", false, "既存のスキルを確認なしで上書きする")
	bindSkillFlag(c, &opts.skills, "これらのスキルだけをインストールする")
	bindDirFlag(c, &opts.dir)

	return c
}

func runAdd(cmd *cobra.Command, source string, opts addOptions) error {
	ctx := cmd.Context()
	layout := lock.NewLayout(opts.dir)

	ref, err := skill.ParseSource(source)
	if err != nil {
		return err
	}

	tracked, err := installsTracked(layout, ref)
	if err != nil {
		return err
	}

	cache := git.NewCache()
	defer func() { _ = cache.Close() }()

	show := status(cmd.ErrOrStderr())
	show("取得中: " + ref.CanonicalSource)
	snap, err := fetchSource(ctx, cache, ref, tracked)

	show("")

	if err != nil {
		return err
	}

	discovered, err := skill.DiscoverSkills(snap.FS)
	if err != nil {
		return fmt.Errorf("スキルの探索: %w", err)
	}

	discovered = skill.FilterDiscoveredSkills(discovered, opts.skills)
	if len(discovered) == 0 {
		return errors.New("インストールするスキルが見つかりません")
	}

	overwrite := printInstallPlan(cmd.OutOrStdout(), layout, discovered)

	if opts.dryRun {
		return nil
	}

	if overwrite {
		if err := approve(cmd, opts.yes, "既存のスキルを上書きしますか?"); err != nil {
			return err
		}
	}

	if tracked {
		return skill.InstallTrackedSkills(discovered, snap, ref, layout)
	}

	return skill.InstallSkills(discovered, ref, layout)
}

func fetchSource(ctx context.Context, cache *git.Cache, ref skill.SourceRef, tracked bool) (git.Snapshot, error) {
	resolve := git.ResolveDefault
	if tracked {
		resolve = git.ResolveLatest
	}

	resolved, err := resolve(ctx, ref.CloneURL, ref.Ref)
	if err != nil {
		return git.Snapshot{}, fmt.Errorf("%s の ref の解決: %w", ref.CanonicalSource, err)
	}

	// ロックファイルには vercel-labs/skills が clone したとおりのファイルのハッシュを記録し、
	// gh skill のメタデータには git ツリーを保存されたまま読んで記録する
	if !tracked {
		fsys, err := cache.Checkout(ctx, ref.CloneURL, resolved)
		if err != nil {
			return git.Snapshot{}, fmt.Errorf("%s の取得: %w", ref.CanonicalSource, err)
		}

		return git.Snapshot{Ref: resolved, FS: fsys}, nil
	}

	snap, err := cache.Fetch(ctx, ref.CloneURL, resolved)
	if err != nil {
		return git.Snapshot{}, fmt.Errorf("%s の取得: %w", ref.CanonicalSource, err)
	}

	return snap, nil
}

func installsTracked(layout lock.Layout, ref skill.SourceRef) (bool, error) {
	lockfileExists := false

	if layout.UsesLockfile() {
		_, err := lock.ReadFile(layout.LockPath())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}

		lockfileExists = err == nil
	}

	tracked := skill.PreferTrackedInstall(lockfileExists, ref.SourceType)
	if !tracked && !layout.UsesLockfile() {
		return false, fmt.Errorf("%s のソースは %q に追加できません: skills-lock.json に記録できるのは %s のみです", ref.SourceType, layout.Dir, lock.DefaultDir)
	}

	return tracked, nil
}

func printInstallPlan(w io.Writer, layout lock.Layout, skills []skill.DiscoveredSkill) bool {
	overwrite := false

	for _, s := range skills {
		action := "インストール"
		if _, err := os.Stat(layout.SkillDir(s.Name)); err == nil {
			action = "上書き"
			overwrite = true
		}

		fmt.Fprintf(w, "%s %s\n", action, layout.SkillDir(s.Name))
	}

	return overwrite
}
