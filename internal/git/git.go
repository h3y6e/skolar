// Package git は skolar がリモートリポジトリに対して必要とする git 操作を実行する
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func gitCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git が見つかりません: %w", err)
	}

	cmd := exec.CommandContext(ctx, gitPath, args...)
	// 強制終了せず中断シグナルを送れば git は自身のロックファイルを削除できる
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second

	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")

	return cmd, nil
}

func runGit(ctx context.Context, args ...string) ([]byte, error) {
	cmd, err := gitCommand(ctx, args...)
	if err != nil {
		return nil, err
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %v: %w\n%s", args, err, out)
	}

	return out, nil
}
