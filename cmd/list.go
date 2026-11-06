package cmd

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/spf13/cobra"
)

type listEntryJSON struct {
	SkillName            string          `json:"skillName"`
	Source               string          `json:"source"`
	Ref                  string          `json:"ref,omitempty"`
	SourceType           lock.SourceType `json:"sourceType"`
	ComputedHash         string          `json:"computedHash"`
	addArg, installedRef string
}

type listOptions struct {
	dir  string
	json bool
}

func newListCmd() *cobra.Command {
	var opts listOptions

	c := &cobra.Command{
		Use:         "list",
		Aliases:     []string{"ls"},
		Annotations: map[string]string{recordKey: "skills"},
		Short:       "インストール済みのスキルを一覧表示する",
		Args:        usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, opts)
		},
	}

	bindDirFlag(c, &opts.dir)
	c.Flags().BoolVar(&opts.json, "json", false, "結果を JSON 形式で出力する")

	return c
}

func runList(cmd *cobra.Command, opts listOptions) (err error) {
	payload := []listEntryJSON{}

	if opts.json {
		defer func() { err = writeRecord(cmd.OutOrStdout(), cmd.Annotations[recordKey], payload, err) }()
	}

	inv, err := loadInventory(opts.dir)
	if err != nil {
		return err
	}

	for _, name := range slices.Sorted(maps.Keys(inv.entries)) {
		entry := inv.entries[name]
		payload = append(payload, listEntryJSON{
			SkillName:    name,
			Source:       entry.Source,
			Ref:          entry.Ref,
			SourceType:   entry.SourceType,
			ComputedHash: entry.ComputedHash,
			addArg:       skill.SourceInput(entry),
		})
	}

	for _, s := range inv.foreign {
		payload = append(payload, listEntryJSON{
			SkillName:    s.Name,
			Source:       foreignSource(s.Meta),
			Ref:          s.Meta.Ref,
			SourceType:   foreignSourceType(s.Meta),
			ComputedHash: s.Meta.TreeSHA,
			addArg:       skill.FormatSourceInput(foreignSource(s.Meta), s.Meta.Pinned),
			installedRef: shortRef(s.Meta.Ref),
		})
	}

	slices.SortFunc(payload, func(a, b listEntryJSON) int {
		return cmp.Compare(a.SkillName, b.SkillName)
	})

	if opts.json {
		return nil
	}

	if len(payload) == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "インストール済みのスキルがありません")
		return nil
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, e := range payload {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.SkillName, e.addArg, e.installedRef, shortHash(e.ComputedHash))
	}

	return tw.Flush()
}

func shortRef(ref string) string {
	for _, prefix := range []string{"refs/heads/", "refs/tags/"} {
		if name, ok := strings.CutPrefix(ref, prefix); ok {
			return name
		}
	}

	return shortHash(ref)
}

func foreignSource(meta skill.SourceMetadata) string {
	if meta.IsGitHub() {
		if source, err := skill.OwnerRepoFromURL(meta.RepoURL); err == nil {
			return source
		}

		return meta.RepoURL
	}

	return meta.LocalPath
}

func foreignSourceType(meta skill.SourceMetadata) lock.SourceType {
	if meta.IsGitHub() {
		return lock.SourceGitHub
	}

	return lock.SourceLocal
}
