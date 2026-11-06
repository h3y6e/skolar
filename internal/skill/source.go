package skill

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/h3y6e/skolar/internal/lock"
)

var shorthandPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// SourceRef は解析済みのスキルのソースを保持する
type SourceRef struct {
	Raw             string
	SourceType      lock.SourceType
	CanonicalSource string
	CloneURL        string
	Ref             string
}

// ParseSource はソースの文字列を SourceRef に解析する。
// GitHub/GitLab の省略記法、SCP 記法、HTTP(S)/SSH の URL に対応する
func ParseSource(raw string) (SourceRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SourceRef{}, errors.New("ソースの解析: ソースが空です")
	}

	location, ref := splitSourceFragment(raw)

	sourceType, canonical, cloneURL, err := parseLocation(location)
	if err != nil {
		return SourceRef{}, err
	}

	return SourceRef{Raw: raw, SourceType: sourceType, CanonicalSource: canonical, CloneURL: cloneURL, Ref: ref}, nil
}

func parseLocation(location string) (lock.SourceType, string, string, error) {
	if looksLikeLocalPath(location) {
		// git は相対パスを実行場所から解決し、その場所はキャッシュである
		abs, err := filepath.Abs(location)

		return lock.SourceLocal, filepath.Clean(location), abs, err
	}

	if shorthandPattern.MatchString(location) {
		return lock.SourceGitHub, location, "https://github.com/" + location + ".git", nil
	}

	if rest, found := strings.CutPrefix(location, "git@"); found {
		return parseSCPLocation(location, rest)
	}

	u, err := url.Parse(location)
	if err != nil {
		return "", "", "", fmt.Errorf("ソース %q の解析: %w", location, err)
	}

	switch u.Scheme {
	case "http", "https", "ssh":
		return parseURLLocation(location, u)
	case "file":
		return lock.SourceLocal, location, location, nil
	}

	return "", "", "", fmt.Errorf("ソース %q の解析: 未対応の形式です", location)
}

// SourceInput はロックファイルのエントリを再インストールする add の引数を返す
func SourceInput(entry lock.Entry) string {
	source := entry.Source
	if entry.SourceType == lock.SourceGitLab {
		source = "https://gitlab.com/" + source
	}

	return FormatSourceInput(source, entry.Ref)
}

// FormatSourceInput はソースと任意の ref を連結して add の引数にする
func FormatSourceInput(source, ref string) string {
	if ref == "" {
		return source
	}

	return source + "#" + ref
}

// RepoURLForSource は "owner/repo" 形式の GitHub ソースについて gh skill がスキルの
// メタデータに記録するものと同じ正規のリポジトリ URL を返す
func RepoURLForSource(canonicalSource string) string {
	return "https://github.com/" + canonicalSource
}

// OwnerRepoFromURL は gh 互換ツールがスキルのメタデータに記録した GitHub リポジトリの
// URL から "owner/repo" 形式を取り出す
func OwnerRepoFromURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("リポジトリ URL %q の解析: %w", rawURL, err)
	}

	if u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") {
		return "", fmt.Errorf("リポジトリ URL %q の解析: https://github.com のみ対応しています", rawURL)
	}

	path := strings.Trim(strings.TrimSuffix(u.EscapedPath(), ".git"), "/")

	segments := splitPathSegments(path)
	if len(segments) != 2 {
		return "", fmt.Errorf("リポジトリ URL %q の解析: owner/repo 形式のパスが必要です", rawURL)
	}

	return strings.Join(segments, "/"), nil
}

func splitSourceFragment(raw string) (string, string) {
	hashIndex := strings.LastIndex(raw, "#")
	if hashIndex <= 0 || hashIndex == len(raw)-1 {
		return raw, ""
	}

	return raw[:hashIndex], raw[hashIndex+1:]
}

func parseSCPLocation(location, rest string) (lock.SourceType, string, string, error) {
	host, path, ok := strings.Cut(rest, ":")
	if !ok || host == "" || path == "" {
		return "", "", "", fmt.Errorf("ソース %q の解析: scp 形式の URL が不正です", location)
	}

	switch host {
	case "github.com":
		return lock.SourceGitHub, strings.TrimSuffix(path, ".git"), location, nil
	case "gitlab.com":
		return lock.SourceGitLab, strings.TrimSuffix(path, ".git"), location, nil
	}

	return lock.SourceGit, location, location, nil
}

func parseURLLocation(location string, u *url.URL) (lock.SourceType, string, string, error) {
	host := strings.ToLower(u.Hostname())

	path := strings.Trim(strings.TrimSuffix(u.EscapedPath(), ".git"), "/")
	if path == "" {
		return "", "", "", fmt.Errorf("ソース %q の解析: リポジトリのパスがありません", location)
	}

	segments := splitPathSegments(path)

	switch host {
	case "github.com":
		if len(segments) != 2 {
			return "", "", "", fmt.Errorf("ソース %q の解析: 未対応の GitHub URL のパスです", location)
		}

		canonical := strings.Join(segments, "/")

		return lock.SourceGitHub, canonical, "https://" + host + "/" + canonical + ".git", nil
	case "gitlab.com":
		if strings.Contains(path, "/-/") {
			return "", "", "", fmt.Errorf("ソース %q の解析: 未対応の GitLab URL のパスです", location)
		}

		if len(segments) < 2 {
			return "", "", "", fmt.Errorf("ソース %q の解析: リポジトリのパスがありません", location)
		}

		canonical := strings.Join(segments, "/")

		return lock.SourceGitLab, canonical, "https://" + host + "/" + canonical + ".git", nil
	}

	return lock.SourceGit, location, location, nil
}

func splitPathSegments(path string) []string {
	segments := strings.Split(path, "/")

	return slices.DeleteFunc(segments, func(segment string) bool {
		return segment == ""
	})
}

func looksLikeLocalPath(raw string) bool {
	if filepath.IsAbs(raw) {
		return true
	}

	return raw == "." || raw == ".." ||
		strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../")
}
