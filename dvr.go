package main

// DVR mode: browse channel → show → season → episode and watch anything on
// demand. Rabbit Ears remembers how far you got in each episode.
//
// While an episode plays, tvplay owns the buttons (d-pad seeks, A pauses,
// L/R jump 10 minutes) and reports where it got to; B stops.

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Node struct {
	Name   string
	Kids   []*Node
	Item   *Item
	Ch     *Channel
	Parent *Node
}

func (n *Node) isFolder() bool { return n.Item == nil }

// episodes lists every video under n.
func (n *Node) episodes(out []*Item) []*Item {
	if n.Item != nil {
		return append(out, n.Item)
	}
	for _, k := range n.Kids {
		out = k.episodes(out)
	}
	return out
}

type Mark struct {
	Pos  float64 `json:"p,omitempty"`
	Done bool    `json:"d,omitempty"`
	When int64   `json:"t,omitempty"`
}

type level struct {
	node     *Node
	sel, top int
}

const (
	dvrNone = iota
	dvrPlay
	dvrPlayFromStart
	dvrLive
	dvrMenu
)

const dvrRows = 5

type DVR struct {
	path    string
	tvDir   string
	marks   map[string]Mark
	root    *Node
	nodeOf  map[*Item]*Node
	stack   []level
	message string

	playing *Node
	from    float64 // where it started playing
}

func newDVR(path string) *DVR {
	d := &DVR{path: path, marks: map[string]Mark{}, root: &Node{Name: "RECORDINGS"}}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &d.marks)
	}
	d.stack = []level{{node: d.root}}
	return d
}

func (d *DVR) save() {
	if d.path == "" {
		return
	}
	if b, err := json.Marshal(d.marks); err == nil {
		tmp := d.path + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, d.path)
		}
	}
}

func (d *DVR) key(it *Item) string {
	if rel, err := filepath.Rel(d.tvDir, it.Path); err == nil {
		return filepath.ToSlash(rel)
	}
	return it.Path
}

func (d *DVR) mark(it *Item) Mark { return d.marks[d.key(it)] }

// setLineup rebuilds the browser tree, keeping the place you were at.
func (d *DVR) setLineup(tvDir string, chans []*Channel) {
	d.tvDir = tvDir
	var where []string
	for _, l := range d.stack[1:] {
		where = append(where, l.node.Name)
	}
	d.root = &Node{Name: "RECORDINGS"}
	d.nodeOf = map[*Item]*Node{}
	for _, ch := range chans {
		cn := &Node{Name: fmt.Sprintf("%d  %s", ch.Num, ch.Name), Ch: ch, Parent: d.root}
		d.root.Kids = append(d.root.Kids, cn)
		if ch.Dir == "" {
			cn.Item = ch.Items[0]
			d.nodeOf[cn.Item] = cn
			continue
		}
		items := append([]*Item(nil), ch.Items...)
		sort.SliceStable(items, func(i, j int) bool { return natLess(items[i].Rel, items[j].Rel) })
		for _, it := range items {
			parts := strings.Split(it.Rel, "/")
			at := cn
			for _, p := range parts[:len(parts)-1] {
				_, nm := splitNumber(p)
				nm = cleanFolder(nm)
				var next *Node
				for _, k := range at.Kids {
					if k.isFolder() && k.Name == nm {
						next = k
					}
				}
				if next == nil {
					next = &Node{Name: nm, Ch: ch, Parent: at}
					at.Kids = append(at.Kids, next)
				}
				at = next
			}
			n := &Node{Name: it.Title, Item: it, Ch: ch, Parent: at}
			at.Kids = append(at.Kids, n)
			d.nodeOf[it] = n
		}
	}
	// back to where we were, as far as it still exists
	d.stack = []level{{node: d.root}}
	for _, name := range where {
		cur := d.stack[len(d.stack)-1].node
		found := -1
		for i, k := range cur.Kids {
			if k.isFolder() && k.Name == name {
				found = i
			}
		}
		if found < 0 {
			break
		}
		d.stack[len(d.stack)-1].sel = found
		d.stack = append(d.stack, level{node: cur.Kids[found]})
	}
}

func (d *DVR) top() *level { return &d.stack[len(d.stack)-1] }

func (d *DVR) selected() *Node {
	l := d.top()
	if l.sel < 0 || l.sel >= len(l.node.Kids) {
		return nil
	}
	return l.node.Kids[l.sel]
}

// focus opens the folder holding n and selects it.
func (d *DVR) focus(n *Node) {
	var chain []*Node
	for p := n; p != nil; p = p.Parent {
		chain = append([]*Node{p}, chain...)
	}
	if len(chain) == 0 || chain[0] != d.root {
		return
	}
	d.stack = nil
	for i := 0; i < len(chain)-1; i++ {
		l := level{node: chain[i]}
		for j, k := range chain[i].Kids {
			if k == chain[i+1] {
				l.sel = j
			}
		}
		d.stack = append(d.stack, l)
	}
	d.fixScroll()
}

func (d *DVR) fixScroll() {
	l := d.top()
	if l.sel < l.top {
		l.top = l.sel
	}
	if l.sel >= l.top+dvrRows {
		l.top = l.sel - dvrRows + 1
	}
	if l.top < 0 {
		l.top = 0
	}
}

// browserKey handles a button in the browser and says what to do next.
func (d *DVR) browserKey(k int) int {
	d.message = ""
	l := d.top()
	n := len(l.node.Kids)
	switch k {
	case BtnUp:
		if n > 0 {
			l.sel = (l.sel - 1 + n) % n
		}
	case BtnDown:
		if n > 0 {
			l.sel = (l.sel + 1) % n
		}
	case BtnL1, BtnLeft:
		l.sel = max(0, l.sel-dvrRows)
	case BtnR1, BtnRight:
		l.sel = min(n-1, l.sel+dvrRows)
	case BtnA:
		s := d.selected()
		if s == nil {
			break
		}
		if s.isFolder() {
			d.stack = append(d.stack, level{node: s})
			break
		}
		return dvrPlay
	case BtnY:
		if s := d.selected(); s != nil && !s.isFolder() {
			return dvrPlayFromStart
		}
	case BtnX:
		s := d.selected()
		if s == nil {
			break
		}
		eps := s.episodes(nil)
		all := true
		for _, it := range eps {
			all = all && d.mark(it).Done
		}
		for _, it := range eps {
			if all {
				delete(d.marks, d.key(it))
			} else {
				d.marks[d.key(it)] = Mark{Done: true, When: time.Now().Unix()}
			}
		}
		d.save()
	case BtnB:
		if len(d.stack) > 1 {
			d.stack = d.stack[:len(d.stack)-1]
		}
	case BtnSelect:
		return dvrLive
	case BtnStart:
		return dvrMenu
	}
	d.fixScroll()
	return dvrNone
}

// resumeAt is where an episode should start: where you stopped, a few
// seconds early, unless you'd nearly finished it.
func (d *DVR) resumeAt(it *Item) float64 {
	m := d.mark(it)
	if m.Done || m.Pos < 20 {
		return 0
	}
	if it.Dur > 0 && m.Pos > it.Dur-45 {
		return 0
	}
	return m.Pos - 3
}

func (d *DVR) startPlaying(n *Node, ss float64) {
	d.playing = n
	d.from = ss
}

// stopPlaying records where you got to (finished: the episode played out;
// pos < 0: tvplay didn't say).
func (d *DVR) stopPlaying(finished bool, pos float64) {
	n := d.playing
	if n == nil {
		return
	}
	d.playing = nil
	k := d.key(n.Item)
	if finished {
		d.marks[k] = Mark{Done: true, When: time.Now().Unix()}
	} else {
		if pos < 0 {
			pos = d.from
		}
		m := d.marks[k]
		m.Pos, m.When = pos, time.Now().Unix()
		if n.Item.Dur > 0 && pos > n.Item.Dur-45 {
			m.Done, m.Pos = true, 0
		}
		d.marks[k] = m
	}
	d.save()
	d.focus(n)
}

// nextAfter: the next episode of the same show, across season folders.
func (d *DVR) nextAfter(n *Node) *Node {
	if n == nil || n.Ch == nil || n.Ch.Dir == "" {
		return nil
	}
	items := append([]*Item(nil), n.Ch.Items...)
	sort.SliceStable(items, func(i, j int) bool { return natLess(items[i].Rel, items[j].Rel) })
	for i, it := range items {
		if it != n.Item {
			continue
		}
		for _, nx := range items[i+1:] {
			if nx.Show == it.Show {
				return d.nodeOf[nx]
			}
		}
	}
	return nil
}

// ---------- drawing ----------

// drawTape: a little VHS cassette for folders (label lit if there's
// something new inside).
func drawTape(c *Canvas, x, y int, fresh bool) {
	c.Fill(x, y, 30, 20, rgb(0x14, 0x14, 0x18))
	lab := guideDim
	if fresh {
		lab = guideCyan
	}
	c.Fill(x+4, y+3, 22, 6, lab)
	c.Circle(x+9, y+14, 3, rgb(0x60, 0x60, 0x68), true, 0)
	c.Circle(x+21, y+14, 3, rgb(0x60, 0x60, 0x68), true, 0)
}

func drawCheck(c *Canvas, x, y int, col color.RGBA) {
	for i := 0; i < 5; i++ {
		c.Fill(x+i, y+6+i, 3, 3, col)
	}
	for i := 0; i < 9; i++ {
		c.Fill(x+5+i, y+10-i, 3, 3, col)
	}
}
