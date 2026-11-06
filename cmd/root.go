// Package cmd は skolar のコマンドラインを実装する
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"

	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	cobra_usage "github.com/jdx/usage/integrations/cobra"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// NewUpstreamFuncs はテストで差し替えられる (IsTTY と同じ方式)
var NewUpstreamFuncs = skill.NewUpstreamFuncs

// NewRootCmd は skolar のコマンドツリーを組み立てる
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "skolar",
		Short: "プロジェクトで使うエージェントスキルを管理する",
		Long: `プロジェクトで使うエージェントスキルを管理する

--help と --version を除くフラグは環境変数でも設定できる (例: --dir は SKOLAR_DIR)`,
		Version:       resolveVersion(version),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return c.Help()
			}

			msg := fmt.Sprintf("%[2]q に %[1]q というコマンドはありません", args[0], c.CommandPath())
			if s := c.SuggestionsFor(args[0]); len(s) > 0 {
				msg += "\n\nもしかして:\n\t" + strings.Join(s, "\n\t")
			}

			return usageError(c, errors.New(msg))
		},
	}
	root.SetFlagErrorFunc(usageError)

	root.AddCommand(newAddCmd())
	root.AddCommand(newUpdateCmd())
	root.AddCommand(newListCmd())
	root.AddCommand(newRemoveCmd())
	root.AddCommand(newVerifyCmd())

	return root
}

// Execute は CLI を実行し、プロセスの終了ステータスを返す
func Execute(version string) int {
	root := NewRootCmd(version)
	if slices.Contains(os.Args[1:], "--usage-spec") {
		fmt.Print(cobra_usage.Generate(root))
		return 0
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	sigs := make(chan os.Signal, 1)

	for _, s := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		// 継承された無視設定 (バックグラウンドジョブなど) は維持する。外すと reraise が戻らなくなる
		if !signal.Ignored(s) {
			signal.Notify(sigs, s)
		}
	}

	go func() { cancel(signalError{<-sigs}) }()

	c, err := root, applyEnv(root)
	if err == nil {
		c, err = root.ExecuteContextC(ctx)
	}

	if sig, ok := errors.AsType[signalError](context.Cause(ctx)); ok {
		reraise(sig.Signal)
	}

	if err == nil {
		return 0
	}

	rec := newErrorRecord(err)
	if key := c.Annotations[recordKey]; key != "" && rec.Code == codeUsage {
		if asJSON, _ := c.Flags().GetBool("json"); asJSON {
			_ = writeRecord(c.OutOrStdout(), key, []any{}, err)
		}
	}

	fmt.Fprintf(os.Stderr, "error: %s\n", rec.Message)

	if rec.Fix != "" {
		fmt.Fprintf(os.Stderr, "fix: %s\n", rec.Fix)
	}

	if rec.Code == codeUsage || rec.Code == codeConfirmationRequired {
		return 2
	}

	return 1
}

const recordKey = "skolar/record"

type signalError struct{ os.Signal }

func (e signalError) Error() string { return e.String() + " を受信しました" }

func reraise(sig os.Signal) {
	signal.Reset()

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return
	}

	if p.Signal(sig) == nil {
		select {}
	}
}

func applyEnv(root *cobra.Command) error {
	commands := []*cobra.Command{root}
	for len(commands) > 0 {
		c := commands[0]
		commands = append(commands[1:], c.Commands()...)

		var err error

		c.Flags().VisitAll(func(f *pflag.Flag) {
			name := "SKOLAR_" + strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))

			value := os.Getenv(name)
			if err != nil || value == "" {
				return
			}

			if slice, ok := f.Value.(pflag.SliceValue); ok {
				err = slice.Replace(strings.Split(value, ","))
			} else {
				err = f.Value.Set(value)
			}

			if err != nil {
				err = usageError(root, fmt.Errorf("%s: %w", name, err))
				return
			}

			f.DefValue = f.Value.String()
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func printSkippedEntries(w io.Writer, skipped []skill.SkippedEntry) {
	for _, s := range skipped {
		fmt.Fprintf(w, "warning: %q をスキップします (未対応のソース種別 %q)\n", s.SkillName, s.SourceType)
	}
}

func bindSkillFlag(c *cobra.Command, target *[]string, usage string) {
	c.Flags().StringSliceVarP(target, "skill", "s", nil, usage+"。'spec-*' のような glob でも指定できる (複数指定可)")
	c.PreRunE = func(c *cobra.Command, _ []string) error {
		if err := skill.CheckPatterns(*target); err != nil {
			return usageError(c, err)
		}

		return nil
	}
}

func bindDirFlag(c *cobra.Command, target *string) {
	c.Flags().StringVarP(target, "dir", "d", lock.DefaultDir, "スキルのディレクトリ")
}

type inventory struct {
	layout   lock.Layout
	lockFile lock.File
	entries  map[string]lock.Entry
	foreign  []skill.InstalledSkill
}

func loadInventory(dir string) (inventory, error) {
	layout := lock.NewLayout(dir)

	lf, err := layout.ReadLockFile()
	if err != nil {
		return inventory{}, err
	}

	installed, err := skill.ScanInstalledSkills(layout.Dir)
	if err != nil {
		return inventory{}, err
	}

	entries := layout.Entries(lf)

	return inventory{layout: layout, lockFile: lf, entries: entries, foreign: skill.UnmanagedSkills(installed, entries)}, nil
}

func (inv inventory) locked(name string) bool {
	_, ok := inv.entries[name]
	return ok
}

func (inv inventory) names() []string {
	names := slices.Collect(maps.Keys(inv.entries))
	for _, s := range inv.foreign {
		names = append(names, s.Name)
	}

	slices.Sort(names)

	return slices.Compact(names)
}

func (inv inventory) recorded(name string) bool {
	return inv.locked(name) || slices.ContainsFunc(inv.foreign, func(s skill.InstalledSkill) bool { return s.Name == name })
}

func resolveVersion(version string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}

	if version == "dev" {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			version = v
		}
	}

	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return version + " (" + s.Value[:7] + ")"
		}
	}

	return version
}
