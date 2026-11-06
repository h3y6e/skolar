// skolar コマンドはプロジェクトで使うエージェントスキルを git リポジトリから取り込んで管理する
package main

import (
	"os"

	"github.com/h3y6e/skolar/cmd"
)

var version = "dev"

func main() {
	os.Exit(cmd.Execute(version))
}
