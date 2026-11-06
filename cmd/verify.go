package cmd

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/spf13/cobra"
)

type verifyOptions struct {
	dir    string
	skills []string
	json   bool
}

func newVerifyCmd() *cobra.Command {
	var opts verifyOptions

	c := &cobra.Command{
		Use:   "verify",
		Short: "インストール済みのスキルを記録と照合する",
		Long: `インストール済みのスキルを記録と照合する

変更されたスキルは記録と異なるファイルも表示する

スキルが変更されているか検証できない場合は終了ステータス 1 で終了する`,
		Annotations: map[string]string{recordKey: "results"},
		Args:        usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVerify(cmd, opts)
		},
	}

	bindSkillFlag(c, &opts.skills, "これらのスキルだけを検証する")
	bindDirFlag(c, &opts.dir)
	c.Flags().BoolVar(&opts.json, "json", false, "結果を JSON 形式で出力する")

	return c
}

func runVerify(cmd *cobra.Command, opts verifyOptions) (err error) {
	results := []skill.VerifyResult{}

	if opts.json {
		defer func() { err = writeRecord(cmd.OutOrStdout(), cmd.Annotations[recordKey], results, err) }()
	}

	inv, err := loadInventory(opts.dir)
	if err != nil {
		return err
	}

	layout, entries, foreign := inv.layout, maps.Clone(inv.entries), inv.foreign

	unrecorded, err := skill.VerifyUnrecorded(layout.Dir, inv.recorded)
	if err != nil {
		return err
	}

	if skillFilter := opts.skills; len(skillFilter) > 0 {
		maps.DeleteFunc(entries, func(name string, _ lock.Entry) bool { return !skill.Selected(skillFilter, name) })
		foreign = slices.DeleteFunc(foreign, func(s skill.InstalledSkill) bool { return !skill.Selected(skillFilter, s.Name) })
		unrecorded = slices.DeleteFunc(unrecorded, func(r skill.VerifyResult) bool { return !skill.Selected(skillFilter, r.SkillName) })
	}

	cache := git.NewCache()
	defer func() { _ = cache.Close() }()

	fns, cloneFn := NewUpstreamFuncs(cmd.Context(), cache), skill.NewCloneFunc(cmd.Context(), cache)

	all := slices.Concat(skill.VerifyLocked(layout, entries, cloneFn), skill.VerifyTracked(foreign, fns), unrecorded)
	slices.SortFunc(all, func(a, b skill.VerifyResult) int { return cmp.Compare(a.SkillName, b.SkillName) })

	var failed []skill.VerifyResult

	for _, r := range all {
		results = append(results, r)
		if !opts.json {
			renderVerifyResult(cmd.OutOrStdout(), r)
		}

		if r.Status == skill.VerifyModified || r.Status == skill.VerifyUnverifiable {
			failed = append(failed, r)
		}
	}

	if len(results) == 0 && !opts.json {
		fmt.Fprintln(cmd.ErrOrStderr(), "検証するインストール済みのスキルがありません")
	}

	return verifyFailed(failed, layout.Dir)
}

var verifyLabels = map[skill.VerifyStatus]string{
	skill.VerifyVerified:     "検証済み",
	skill.VerifyModified:     "変更あり",
	skill.VerifyUnverifiable: "検証不能",
	skill.VerifyLocal:        "ローカル",
}

func renderVerifyResult(w io.Writer, r skill.VerifyResult) {
	label := verifyLabels[r.Status]

	switch {
	case len(r.Files) > 0:
		fmt.Fprintf(w, "%s: %s (%s)\n", r.SkillName, label, strings.Join(r.Files, ", "))
	case r.Reason != "":
		fmt.Fprintf(w, "%s: %s (%s)\n", r.SkillName, label, r.Reason)
	default:
		fmt.Fprintf(w, "%s: %s\n", r.SkillName, label)
	}
}

func verifyFailed(failed []skill.VerifyResult, dir string) error {
	if len(failed) == 0 {
		return nil
	}

	names := make([]string, len(failed))
	for i, r := range failed {
		names[i] = r.SkillName
	}

	var fix string
	if i := slices.IndexFunc(failed, func(r skill.VerifyResult) bool { return r.Reinstall != "" }); i >= 0 {
		fix = fmt.Sprintf("skolar add %s -s %s --dir %s -y", failed[i].Reinstall, failed[i].SkillName, dir)
	}

	return &cliError{
		code: codeVerifyFailed,
		err:  fmt.Errorf("%s を検証できませんでした", strings.Join(names, ", ")),
		fix:  fix,
	}
}
