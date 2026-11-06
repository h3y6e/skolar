package cmd

import (
	"fmt"
	"os"
	"slices"

	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/spf13/cobra"
)

type removeOptions struct {
	dir    string
	dryRun bool
	yes    bool
}

func newRemoveCmd() *cobra.Command {
	var opts removeOptions

	c := &cobra.Command{
		Use:     "remove <skill>...",
		Aliases: []string{"rm"},
		Short:   "インストール済みのスキルを削除する (破壊的)",
		Long: `インストール済みのスキルを削除する (破壊的)

'spec-*' のような glob でもインストール済みのスキルを指定できる`,
		Args: usageArgs(cobra.MatchAll(cobra.MinimumNArgs(1), func(_ *cobra.Command, args []string) error {
			return skill.CheckPatterns(args)
		})),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd, args, opts)
		},
	}

	bindDirFlag(c, &opts.dir)
	c.Flags().BoolVarP(&opts.yes, "yes", "y", false, "確認なしで削除する")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "削除されるディレクトリを表示する")

	return c
}

func runRemove(cmd *cobra.Command, patterns []string, opts removeOptions) error {
	inv, err := loadInventory(opts.dir)
	if err != nil {
		return err
	}

	var skillNames []string

	for _, p := range patterns {
		matched := slices.DeleteFunc(inv.names(), func(name string) bool { return !skill.Selected([]string{p}, name) })
		if len(matched) == 0 {
			return &cliError{
				code: codeSkillNotFound,
				err:  fmt.Errorf("スキル %q が %q に見つかりません", p, inv.layout.Dir),
				fix:  "skolar list --dir " + inv.layout.Dir,
			}
		}

		skillNames = append(skillNames, matched...)
	}

	slices.Sort(skillNames)
	skillNames = slices.Compact(skillNames)

	for _, name := range skillNames {
		fmt.Fprintf(cmd.OutOrStdout(), "削除 %s\n", inv.layout.SkillDir(name))
	}

	if opts.dryRun {
		return nil
	}

	if err := approve(cmd, opts.yes, "これらのスキルを削除しますか?"); err != nil {
		return err
	}

	for _, name := range skillNames {
		if err := os.RemoveAll(inv.layout.SkillDir(name)); err != nil {
			return fmt.Errorf("スキルディレクトリ %q の削除: %w", name, err)
		}
	}

	if !slices.ContainsFunc(skillNames, inv.locked) {
		return nil
	}

	for _, name := range skillNames {
		delete(inv.lockFile.Skills, name)
	}

	if err := lock.WriteFile(inv.layout.LockPath(), inv.lockFile); err != nil {
		return fmt.Errorf("ロックファイルの書き込み: %w", err)
	}

	return nil
}
