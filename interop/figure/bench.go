package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

const (
	axisX     = 200
	axisWidth = 520
)

var titles = map[string]string{
	"Install/vercel": "skills-lock.json プロジェクトへのインストール",
	"Install/gh":     "gh skill ディレクトリへのインストール",
	"Update/vercel":  "skills-lock.json プロジェクトの更新",
	"Update/gh":      "gh skill ディレクトリの更新",
}

type bar struct {
	Tool         string
	Skolar       bool
	Cold, Warm   float64
	Y            int
	ColdW, WarmW int
}

type group struct {
	Op, Title string
	Y         int
	Bars      []bar
}

type tick struct {
	X     int
	Label string
}

type bench struct {
	Env, Platform, Readout, Versions string
	Runs                             int
	Groups                           []group
	Shown                            []group
	Ticks                            []tick
	RulerY                           int
}

func readBench(r io.Reader) (any, error) {
	vs := versions{}

	env, samples, order, err := parseBench(r, vs)
	if err != nil {
		return nil, err
	}

	line, err := vs.line()
	if err != nil {
		return nil, err
	}

	p := bench{Env: env["goos"] + "/" + env["goarch"] + " · " + env["cpu"], Platform: env["goos"] + "/" + env["goarch"], Versions: line, Runs: len(samples[order[0]])}

	for _, name := range order {
		if err := p.add(name, samples[name]); err != nil {
			return nil, err
		}
	}

	for _, g := range p.Groups {
		if g.Op == "Update" {
			p.Shown = append(p.Shown, g)
		}
	}

	if err := p.summarize(); err != nil {
		return nil, err
	}

	p.layout()

	return p, nil
}

func parseBench(r io.Reader, vs versions) (map[string]string, map[string][]float64, []string, error) {
	env := map[string]string{}
	samples := map[string][]float64{}

	var order []string

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if vs.add(line) {
			continue
		}

		if k, v, ok := strings.Cut(line, ": "); ok && slices.Contains([]string{"goos", "goarch", "cpu"}, k) {
			env[k] = v
			continue
		}

		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") || f[3] != "ns/op" {
			continue
		}

		ns, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			return nil, nil, nil, err
		}

		name := trimProcs(f[0])
		if _, ok := samples[name]; !ok {
			order = append(order, name)
		}

		samples[name] = append(samples[name], ns/1e9)
	}

	if err := sc.Err(); err != nil {
		return nil, nil, nil, err
	}

	if len(order) == 0 {
		return nil, nil, nil, errors.New("入力にベンチマーク結果がありません")
	}

	return env, samples, order, nil
}

func (p *bench) add(name string, samples []float64) error {
	op, params, _ := strings.Cut(strings.TrimPrefix(name, "Benchmark"), "/")

	kv := map[string]string{}

	for param := range strings.SplitSeq(params, "/") {
		k, v, _ := strings.Cut(param, "=")
		kv[k] = v
	}

	title, ok := titles[op+"/"+kv["format"]]
	if !ok {
		return fmt.Errorf("%s: このベンチマークに対応する見出しがありません", name)
	}

	gi := slices.IndexFunc(p.Groups, func(g group) bool { return g.Title == title })
	if gi < 0 {
		gi = len(p.Groups)
		p.Groups = append(p.Groups, group{Op: op, Title: title})
	}

	g := &p.Groups[gi]

	bi := slices.IndexFunc(g.Bars, func(b bar) bool { return b.Tool == display(kv["tool"]) })
	if bi < 0 {
		bi = len(g.Bars)
		g.Bars = append(g.Bars, bar{Tool: display(kv["tool"]), Skolar: kv["tool"] == "skolar"})
	}

	switch kv["cache"] {
	case "cold":
		g.Bars[bi].Cold = median(samples)
	case "warm":
		g.Bars[bi].Warm = median(samples)
	default:
		return fmt.Errorf("%s: キャッシュが cold でも warm でもありません", name)
	}

	p.Runs = min(p.Runs, len(samples))

	return nil
}

func (p *bench) summarize() error {
	lead := math.Inf(1)

	for _, g := range p.Shown {
		if len(g.Bars) != 2 || !g.Bars[0].Skolar {
			return fmt.Errorf("%s: skolar と他のツール 1 つが必要です", g.Title)
		}

		s, o := g.Bars[0], g.Bars[1]
		if r := o.Warm / s.Warm; r < lead {
			lead = r
			p.Readout = fmt.Sprintf("2 回目以降: skolar %s · %s %s", secs(s.Warm), o.Tool, secs(o.Warm))
		}
	}

	return nil
}

func (p *bench) layout() {
	longest := 0.0

	for _, g := range p.Shown {
		for _, b := range g.Bars {
			longest = max(longest, b.Cold, b.Warm)
		}
	}

	span := math.Ceil(longest*2) / 2
	x := func(s float64) int { return int(s / span * axisWidth) }

	y := 84

	for gi := range p.Shown {
		g := &p.Shown[gi]
		g.Y = y
		y += 14

		for bi := range g.Bars {
			b := &g.Bars[bi]
			b.Y = y
			b.ColdW, b.WarmW = x(b.Cold), x(b.Warm)
			y += 26
		}

		y += 18
	}

	p.RulerY = y

	for i := range int(math.Round(span*10)) + 1 {
		label := ""
		if i%5 == 0 {
			label = strconv.FormatFloat(float64(i)/10, 'f', 1, 64) + " s"
		}

		p.Ticks = append(p.Ticks, tick{X: axisX + x(float64(i)/10), Label: label})
	}
}

// trimProcs は go test が付ける -GOMAXPROCS の接尾辞を取り除く。値が 1 のときは付かない
func trimProcs(name string) string {
	i := strings.LastIndex(name, "-")
	if _, err := strconv.Atoi(name[i+1:]); i < 0 || err != nil {
		return name
	}

	return name[:i]
}

func median(xs []float64) float64 {
	s := slices.Sorted(slices.Values(xs))
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}

	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func secs(s float64) string { return strconv.FormatFloat(s, 'f', 2, 64) + " s" }

var benchFuncs = template.FuncMap{
	"secs":  secs,
	"axisX": func() int { return axisX },
	"axisW": func() int { return axisWidth },
	"tickLen": func(label string) int {
		if label == "" {
			return 4
		}

		return 8
	},
}
