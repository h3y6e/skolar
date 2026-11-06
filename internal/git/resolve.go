package git

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// ResolvedRef はリモートリポジトリに対して解決した git ref である
type ResolvedRef struct {
	// Full は完全修飾の ref ("refs/heads/main"、"refs/tags/v1.0.0") である。
	// ref がどのブランチやタグにも一致しなかった場合はコミット SHA そのものである
	Full string
	// Object はリモートが Full について公開するオブジェクトである。コミット、
	// 注釈付きタグならタグオブジェクト、SHA そのものなら Full 自身になる
	Object string
}

// ResolveLatest はタグよりブランチを優先してリモートで ref を解決する。
// ref が空ならプレリリースでない最新の semver タグに解決し、なければ
// gh skill install と同様にリモートのデフォルトブランチに解決する
func ResolveLatest(ctx context.Context, cloneURL, ref string) (ResolvedRef, error) {
	if ref != "" {
		return resolveExplicitRef(ctx, cloneURL, ref)
	}

	refs, err := lsRemote(ctx, cloneURL, "HEAD", "refs/tags/*")
	if err != nil {
		return ResolvedRef{}, err
	}

	if tag := latestTag(refs); tag != "" {
		return ResolvedRef{Full: tag, Object: refs.objects[tag]}, nil
	}

	return refs.head(cloneURL)
}

// ResolveDefault はタグよりブランチを優先してリモートで ref を解決する。
// ref が空なら git clone と同様にリモートのデフォルトブランチに解決する
func ResolveDefault(ctx context.Context, cloneURL, ref string) (ResolvedRef, error) {
	if ref != "" {
		return resolveExplicitRef(ctx, cloneURL, ref)
	}

	refs, err := lsRemote(ctx, cloneURL, "HEAD")
	if err != nil {
		return ResolvedRef{}, err
	}

	return refs.head(cloneURL)
}

func resolveExplicitRef(ctx context.Context, cloneURL, ref string) (ResolvedRef, error) {
	candidates := []string{"refs/heads/" + ref, "refs/tags/" + ref}
	if strings.HasPrefix(ref, "refs/") {
		candidates = append([]string{ref}, candidates...)
	}

	refs, err := lsRemote(ctx, cloneURL, candidates...)
	if err != nil {
		return ResolvedRef{}, err
	}

	for _, full := range candidates {
		if object, ok := refs.objects[full]; ok {
			return ResolvedRef{Full: full, Object: object}, nil
		}
	}

	return ResolvedRef{Full: ref, Object: ref}, nil
}

func latestTag(refs remoteRefs) string {
	latestVersion, latest := "", ""

	for name := range refs.objects {
		tag, found := strings.CutPrefix(name, "refs/tags/")
		if !found || strings.HasSuffix(tag, "^{}") {
			continue
		}

		version := tag
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}

		if !semver.IsValid(version) || semver.Prerelease(version) != "" {
			continue
		}

		if c := semver.Compare(version, latestVersion); latestVersion == "" || c > 0 || c == 0 && name > latest {
			latestVersion, latest = version, name
		}
	}

	return latest
}

type remoteRefs struct {
	headTarget string
	objects    map[string]string
}

func (r remoteRefs) head(cloneURL string) (ResolvedRef, error) {
	if r.headTarget == "" {
		return ResolvedRef{}, fmt.Errorf("git ls-remote %q: デフォルトブランチを特定できません", cloneURL)
	}

	return ResolvedRef{Full: r.headTarget, Object: r.objects["HEAD"]}, nil
}

func lsRemote(ctx context.Context, cloneURL string, patterns ...string) (remoteRefs, error) {
	args := append([]string{"ls-remote", "--symref", "--", cloneURL}, patterns...)

	out, err := runGit(ctx, args...)
	if err != nil {
		return remoteRefs{}, err
	}

	refs := remoteRefs{objects: map[string]string{}}

	for line := range strings.Lines(string(out)) {
		left, name, found := strings.Cut(strings.TrimSpace(line), "\t")
		if !found {
			continue
		}

		if target, ok := strings.CutPrefix(left, "ref: "); ok {
			if name == "HEAD" {
				refs.headTarget = target
			}

			continue
		}

		refs.objects[name] = left
	}

	return refs, nil
}
