// Command figure は標準入力から読んだ interop テストの出力を README に埋め込む図として描画する
//
//	go test -tags interop -count 1 -json ./interop | go run ./interop/figure compat > docs/compat.svg
//	go test -tags interop -run '^$' -bench . -benchtime 1x -count 5 ./interop | go run ./interop/figure bench > docs/bench.svg
package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"text/template"
)

//go:embed *.svg.tmpl
var templates embed.FS

type figure struct {
	read  func(io.Reader) (any, error)
	funcs template.FuncMap
}

var figures = map[string]figure{
	"compat": {readCompat, compatFuncs},
	"bench":  {readBench, benchFuncs},
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, r io.Reader, w io.Writer) error {
	if len(args) != 1 {
		return errors.New("使い方: figure compat|bench")
	}

	f, ok := figures[args[0]]
	if !ok {
		return errors.New("使い方: figure compat|bench")
	}

	data, err := f.read(r)
	if err != nil {
		return err
	}

	funcs := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
	}
	maps.Copy(funcs, f.funcs)

	name := args[0] + ".svg.tmpl"

	t, err := template.New(name).Funcs(funcs).ParseFS(templates, name, "style.svg.tmpl")
	if err != nil {
		return err
	}

	return t.Execute(w, data)
}

func display(tool string) string {
	if tool == "gh" {
		return "gh skill"
	}

	return tool
}

type versions map[string]string

func (v versions) add(line string) bool {
	rest, ok := strings.CutPrefix(line, "version: ")
	if !ok {
		return false
	}

	tool, version, _ := strings.Cut(rest, " ")
	v[tool] = version

	return true
}

func (v versions) line() (string, error) {
	var parts []string

	for _, tool := range []string{"skolar", "skills", "gh"} {
		version, ok := v[tool]
		if !ok {
			return "", fmt.Errorf("入力に %s のバージョンがありません", tool)
		}

		parts = append(parts, display(tool)+" "+version)
	}

	return strings.Join(parts, " · "), nil
}
