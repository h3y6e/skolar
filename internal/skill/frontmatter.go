package skill

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

const frontmatterDelimiter = "---"

// SourceMetadata はインストール済みスキルの出どころを表す。gh 互換ツール (gh skill) が
// SKILL.md のフロントマターに記録したものである
type SourceMetadata struct {
	RepoURL   string // metadata.github-repo。例: "https://github.com/owner/repo"
	Ref       string // metadata.github-ref。例: "refs/heads/main"、"refs/tags/v1.0.0"、コミット SHA
	TreeSHA   string // metadata.github-tree-sha
	Path      string // metadata.github-path。例: "skills/foo"
	Pinned    string // metadata.github-pinned (空なら固定なし)
	LocalPath string // metadata.local-path
}

// IsGitHub はスキルが GitHub リポジトリからインストールされたかを返す
func (m SourceMetadata) IsGitHub() bool { return m.RepoURL != "" }

// IsLocal はスキルがローカルのディレクトリからインストールされたかを返す
func (m SourceMetadata) IsLocal() bool { return m.LocalPath != "" }

// ParseSourceMetadata は SKILL.md の内容から gh 互換のソースのメタデータを読む。
// フロントマターがない内容はエラーなしでゼロ値のメタデータになる
func ParseSourceMetadata(content string) (SourceMetadata, error) {
	raw, _, err := parseFrontmatter(content)
	if err != nil {
		return SourceMetadata{}, err
	}

	meta, _ := raw["metadata"].(map[string]any)

	return SourceMetadata{
		RepoURL:   stringValue(meta, "github-repo"),
		Ref:       stringValue(meta, "github-ref"),
		TreeSHA:   stringValue(meta, "github-tree-sha"),
		Path:      stringValue(meta, "github-path"),
		Pinned:    stringValue(meta, "github-pinned"),
		LocalPath: stringValue(meta, "local-path"),
	}, nil
}

// InjectSourceMetadata は gh 互換のソースのメタデータを SKILL.md の内容にマージし、
// ほかのフロントマターのキーと本文は保つ。フロントマターがなければ作る
func InjectSourceMetadata(content string, m SourceMetadata) (string, error) {
	raw, body, err := parseFrontmatter(content)
	if err != nil {
		return "", err
	}

	if raw == nil {
		raw = make(map[string]any)
	}

	meta, _ := raw["metadata"].(map[string]any)
	if meta == nil {
		meta = make(map[string]any)
	}

	// 古い gh が書いた旧形式のキー
	delete(meta, "github-owner")
	delete(meta, "github-sha")

	if m.IsGitHub() {
		delete(meta, "local-path")
		meta["github-repo"] = m.RepoURL
		meta["github-ref"] = m.Ref
		meta["github-tree-sha"] = m.TreeSHA

		meta["github-path"] = m.Path
		if m.Pinned != "" {
			meta["github-pinned"] = m.Pinned
		} else {
			delete(meta, "github-pinned")
		}
	} else if m.IsLocal() {
		for _, key := range []string{"github-repo", "github-ref", "github-tree-sha", "github-path", "github-pinned"} {
			delete(meta, key)
		}

		meta["local-path"] = m.LocalPath
	}

	raw["metadata"] = meta

	return serializeFrontmatter(raw, body)
}

func stringValue(meta map[string]any, key string) string {
	value, _ := meta[key].(string)
	return value
}

// parseFrontmatter は SKILL.md の内容を YAML フロントマターの map と本文に分ける。
// フロントマターがなければ nil の map と全体を本文として返す。区切りの扱いは
// 注入後のファイルがバイト単位で互換になるよう、意図的に gh skill (cli/cli) に合わせている
func parseFrontmatter(content string) (map[string]any, string, error) {
	trimmed := strings.TrimLeft(content, "\r\n")
	if !strings.HasPrefix(trimmed, frontmatterDelimiter) {
		return nil, content, nil
	}

	rest := strings.TrimLeft(trimmed[len(frontmatterDelimiter):], "\r\n")

	before, after, ok := strings.Cut(rest, "\n"+frontmatterDelimiter)
	if !ok {
		return nil, content, nil
	}

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(before), &raw); err != nil {
		return nil, "", fmt.Errorf("フロントマターの YAML が不正です: %w", err)
	}

	body := strings.TrimLeft(after, "\r\n")

	return raw, body, nil
}

func serializeFrontmatter(raw map[string]any, body string) (string, error) {
	yamlBytes, err := yaml.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("フロントマターのシリアライズ: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(frontmatterDelimiter + "\n")
	buf.Write(yamlBytes)
	buf.WriteString(frontmatterDelimiter + "\n")

	if body != "" {
		buf.WriteString(body)

		if !strings.HasSuffix(body, "\n") {
			buf.WriteString("\n")
		}
	}

	return buf.String(), nil
}
