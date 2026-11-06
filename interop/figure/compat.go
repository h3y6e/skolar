package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/template"
)

type status int

const (
	uncovered status = iota
	pass
	gap
	fail
)

func (s status) String() string { return [...]string{"対象外", "合格", "制約", "失敗"}[s] }

type suite struct {
	tool int
	gap  bool
}

const skolarOnly = -1

var suites = map[string]suite{
	"TestVercelFormat":            {tool: 0},
	"TestGhFormat":                {tool: 1},
	"TestGhUndiscoverableLayouts": {tool: 1, gap: true},
	"TestRecordChoice":            {tool: skolarOnly},
}

type tool struct {
	Name string
	X    int
}

var tools = [2]tool{
	{Name: "skills", X: 600},
	{Name: "gh skill", X: 760},
}

type outcome struct {
	Sentence string
	Status   status
}

type cell struct {
	Status status
	Cases  []outcome
}

type row struct {
	Label string
	Y     int
	Cells [2]cell
}

type compat struct {
	Tools    [2]tool
	Rows     []row
	Card     int
	Readout  string
	Versions string
	index    map[string]int
}

type event struct {
	Action string
	Test   string
	Output string
}

func readCompat(r io.Reader) (any, error) {
	c := compat{Tools: tools, index: map[string]int{}}
	vs := versions{}

	for dec := json.NewDecoder(r); ; {
		var e event
		if err := dec.Decode(&e); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}

		if e.Action == "output" && e.Test == "" && vs.add(strings.TrimSpace(e.Output)) {
			continue
		}

		if err := c.record(e); err != nil {
			return nil, err
		}
	}

	line, err := vs.line()
	if err != nil {
		return nil, err
	}

	c.Versions = line
	c.layout()

	return c, nil
}

func (c *compat) record(e event) error {
	top, sub, ok := strings.Cut(e.Test, "/")
	if !ok || e.Action != "pass" && e.Action != "fail" {
		return nil
	}

	s, ok := suites[top]
	if !ok {
		return fmt.Errorf("%s: このテストに対応するツールがありません", top)
	}

	if s.tool == skolarOnly {
		return nil
	}

	label, sentence, ok := strings.Cut(strings.ReplaceAll(sub, "_", " "), ": ")
	if !ok {
		return fmt.Errorf("%s: サブテスト名に「ラベル: 」の接頭辞がありません", e.Test)
	}

	i, ok := c.index[label]
	if !ok {
		i = len(c.Rows)
		c.index[label] = i
		c.Rows = append(c.Rows, row{Label: label})
	}

	got := pass

	switch {
	case e.Action == "fail":
		got = fail
	case s.gap:
		got = gap
	}

	cl := &c.Rows[i].Cells[s.tool]
	cl.Status = max(cl.Status, got)
	cl.Cases = append(cl.Cases, outcome{Sentence: sentence, Status: got})

	return nil
}

func (c *compat) layout() {
	for i := range c.Rows {
		c.Rows[i].Y = 84 + i*32
	}

	c.Card = 84 + len(c.Rows)*32 + 44

	var counts [4]int

	var gaps [2]int

	for _, r := range c.Rows {
		for ti, cl := range r.Cells {
			for _, o := range cl.Cases {
				counts[o.Status]++
				if o.Status == gap {
					gaps[ti]++
				}
			}
		}
	}

	c.Readout = strconv.Itoa(counts[pass]) + " 件合格"

	for ti, n := range gaps {
		if n > 0 {
			c.Readout += " · " + c.Tools[ti].Name + " に " + strconv.Itoa(n) + " 件の制約"
		}
	}

	if counts[fail] > 0 {
		c.Readout += " · " + strconv.Itoa(counts[fail]) + " 件失敗"
	}
}

var compatFuncs = template.FuncMap{
	"css": func(s status) string {
		return [...]string{"none", "pass", "gap", "fail"}[s]
	},
	"glyph": func(s status) string { return [...]string{"–", "✓", "!", "✕"}[s] },
}
