package git

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ErrNotCached はキャッシュにオブジェクトがないことを表す
var ErrNotCached = errors.New("キャッシュにありません")

// Snapshot はキャッシュから読み取ったリモートリポジトリのコミットである
type Snapshot struct {
	Ref ResolvedRef
	FS  fs.FS
	// TreeSHA はリポジトリ相対ディレクトリの git ツリー SHA を返す。
	// ルートは "." である
	TreeSHA func(dir string) (string, error)
}

// Cache は実行をまたいでリモートリポジトリのオブジェクトを保持する。リモートごとに
// ベアリポジトリを一つ持つため、取得済みのコミットはオフラインで読み取れる。
// Close までは各リポジトリを単一の git プロセスで読み取る
type Cache struct {
	mu        sync.Mutex
	repos     map[string]func() (*repo, error)
	checkouts []*os.Root
}

// NewCache はキャッシュを開く
func NewCache() *Cache {
	return &Cache{repos: map[string]func() (*repo, error){}}
}

// Close はキャッシュの読み取りを止める
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	for _, open := range c.repos {
		if r, err := open(); err == nil {
			errs = append(errs, r.close())
		}
	}

	for _, root := range c.checkouts {
		errs = append(errs, root.Close(), os.RemoveAll(root.Name()))
	}

	return errors.Join(errs...)
}

// Checkout は cloneURL の ref を git checkout が書き出すとおりに読み取る。
// .gitattributes と core.autocrlf の指定に従って改行コードの変換とフィルタを適用し、
// キャッシュにない場合は ref を取得する
func (c *Cache) Checkout(ctx context.Context, cloneURL string, ref ResolvedRef) (fs.FS, error) {
	r, err := c.repo(ctx, cloneURL, objectFormat(ref.Object))
	if err != nil {
		return nil, err
	}

	tree, err := r.fetch(ctx, cloneURL, ref)
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "skolar-checkout-*")
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}

	c.mu.Lock()
	c.checkouts = append(c.checkouts, root)
	c.mu.Unlock()

	// 並行するチェックアウトがリポジトリの index.lock を取り合わないよう専用のインデックスを使う
	restore, err := gitCommand(ctx, "--git-dir", r.gitDir, "--work-tree", dir, "restore", "--source", tree, "--worktree", "--", ":/")
	if err != nil {
		return nil, err
	}

	restore.Env = append(restore.Env, "GIT_INDEX_FILE="+dir+".index")
	defer os.Remove(dir + ".index")

	if out, err := restore.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git restore: %w\n%s", err, out)
	}

	return root.FS(), nil
}

// Fetch は cloneURL の ref を読み取る。キャッシュになければ先に取得する
func (c *Cache) Fetch(ctx context.Context, cloneURL string, ref ResolvedRef) (Snapshot, error) {
	r, err := c.repo(ctx, cloneURL, objectFormat(ref.Object))
	if err != nil {
		return Snapshot{}, err
	}

	tree, err := r.fetch(ctx, cloneURL, ref)
	if err != nil {
		return Snapshot{}, err
	}

	t := r.tree(tree)

	return Snapshot{Ref: ref, FS: t, TreeSHA: t.treeSHA}, nil
}

// Prefetch はキャッシュにない場合に cloneURL の ref を取得する
func (c *Cache) Prefetch(ctx context.Context, cloneURL string, ref ResolvedRef) error {
	r, err := c.repo(ctx, cloneURL, objectFormat(ref.Object))
	if err != nil {
		return err
	}

	_, err = r.fetch(ctx, cloneURL, ref)

	return err
}

// ReadTree は cloneURL のツリー treeSHA をキャッシュからオフラインで読み取る
func (c *Cache) ReadTree(ctx context.Context, cloneURL, treeSHA string) (fs.FS, error) {
	if !isObjectID(treeSHA) {
		return nil, fmt.Errorf("不正なツリー SHA %q", treeSHA)
	}

	r, err := c.repo(ctx, cloneURL, objectFormat(treeSHA))
	if err != nil {
		return nil, err
	}

	if typ, err := r.info(treeSHA); err != nil || typ != "tree" {
		return nil, fmt.Errorf("ツリー %s: %w", treeSHA, ErrNotCached)
	}

	return r.tree(treeSHA), nil
}

func (c *Cache) repo(ctx context.Context, cloneURL, format string) (*repo, error) {
	key := strings.TrimSuffix(strings.TrimSuffix(cloneURL, "/"), ".git") + "\x00" + format

	c.mu.Lock()

	open, ok := c.repos[key]
	if !ok {
		open = sync.OnceValues(func() (*repo, error) { return openRepo(ctx, key, format) })
		c.repos[key] = open
	}

	c.mu.Unlock()

	return open()
}

type repo struct {
	gitDir string
	mu     sync.Mutex
	in     io.WriteCloser
	out    *bufio.Reader
	close  func() error
}

func openRepo(ctx context.Context, key, format string) (*repo, error) {
	base, err := cacheRoot()
	if err != nil {
		return nil, fmt.Errorf("キャッシュの場所の特定: %w", err)
	}

	root := filepath.Join(base, "skolar")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("キャッシュの作成: %w", err)
	}

	sum := sha256.Sum256([]byte(key))
	r := &repo{gitDir: filepath.Join(root, "repos", hex.EncodeToString(sum[:12])+".git")}

	if err := r.init(ctx, format); err != nil {
		return nil, err
	}

	cmd, err := gitCommand(context.WithoutCancel(ctx), "--git-dir", r.gitDir, "cat-file", "--batch-command")
	if err != nil {
		return nil, err
	}

	if r.in, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}

	r.out = bufio.NewReader(stdout)
	r.close = sync.OnceValue(func() error { return errors.Join(r.in.Close(), cmd.Wait()) })

	return r, nil
}

func cacheRoot() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}

	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return os.UserCacheDir()
	}

	home, err := os.UserHomeDir()

	return filepath.Join(home, ".cache"), err
}

func (r *repo) init(ctx context.Context, format string) error {
	unlock, err := lock(ctx, r.gitDir+".lock")
	if err != nil {
		return err
	}
	defer unlock()

	if _, err := os.Stat(filepath.Join(r.gitDir, "HEAD")); !errors.Is(err, os.ErrNotExist) {
		return err
	}

	_, err = runGit(ctx, "init", "--quiet", "--bare", "--object-format="+format, "--", r.gitDir)

	return err
}

func (r *repo) fetch(ctx context.Context, cloneURL string, ref ResolvedRef) (string, error) {
	unlock, err := lock(ctx, r.gitDir+".lock")
	if err != nil {
		return "", err
	}
	defer unlock()

	if isObjectID(ref.Object) {
		if tree, err := r.peelTree(ref.Object); err == nil {
			return tree, nil
		}
	}

	// 取得したバージョンごとに ref を作って verify が使うオブジェクトを gc から守る。
	// 名前を平坦にするのは大文字小文字を区別しないファイルシステムでも
	// ほかの ref と衝突させないためである
	local := ref.Object
	if !isObjectID(local) {
		sum := sha256.Sum256([]byte(ref.Full))
		local = hex.EncodeToString(sum[:12])
	}

	local = "refs/skolar/" + local

	// 取得した内容は常に一つのパックになり、git はパックを丸ごと書き込む。そのため
	// 中断した取得はオブジェクトを残さず、キャッシュ済みのコミットは欠けない
	if _, err := runGit(ctx, "-C", r.gitDir, "-c", "fetch.unpackLimit=1", "-c", "gc.autoDetach=false",
		"fetch", "--quiet", "--depth", "1", "--no-tags", "--no-write-fetch-head", "--", cloneURL, "+"+ref.Full+":"+local); err != nil {
		return "", err
	}

	return r.peelTree(local)
}

func (r *repo) peelTree(object string) (string, error) {
	if _, err := r.info(object + "^{commit}"); err != nil {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	oid, _, err := r.requestLocked("info", object+"^{tree}")

	return oid, err
}

func (r *repo) info(object string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, typ, err := r.requestLocked("info", object)

	return typ, err
}

func (r *repo) contents(oid string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, size, err := r.requestLocked("contents", oid)
	if err != nil {
		return nil, err
	}

	n, err := strconv.Atoi(size)
	if err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}

	data := make([]byte, n+1)
	if _, err := io.ReadFull(r.out, data); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}

	return data[:n], nil
}

func (r *repo) requestLocked(command, object string) (string, string, error) {
	if strings.ContainsAny(object, "\n\r") {
		return "", "", fmt.Errorf("不正なオブジェクト名 %q", object)
	}

	if _, err := fmt.Fprintf(r.in, "%s %s\n", command, object); err != nil {
		return "", "", fmt.Errorf("git cat-file: %w", err)
	}

	header, err := r.out.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("git cat-file: %w", err)
	}

	fields := strings.Fields(header)
	if len(fields) != 3 {
		return "", "", fmt.Errorf("%s: %w", strings.TrimSpace(header), ErrNotCached)
	}

	if command == "info" {
		return fields[0], fields[1], nil
	}

	return fields[0], fields[2], nil
}

func objectFormat(oid string) string {
	if len(oid) == 64 {
		return "sha256"
	}

	return "sha1"
}

func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}

	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}

	return true
}
