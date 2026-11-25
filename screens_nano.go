package main

// The RG Nano's screens: everything laid out for its 1.54" 240x240 panel.
// The look is the Miyoo version's (navy guide, blue SETUP, colour bars),
// with bigger type per pixel so it reads on a screen this small.

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"
)

// hint draws a compact button chip and its caption; returns the x after it.
func hint(c *Canvas, f *Font, x, y int, btn, caption string, chip, text color.RGBA) int {
	bw := max(f.Width(btn)+6, f.height)
	c.Fill(x, y, bw, f.height+1, chip)
	c.Text(f, x+(bw-f.Width(btn))/2, y, btn, colBG)
	x += bw + 3
	x = c.Text(f, x, y, caption, text)
	return x + 7
}

// footer lays out button hints along the bottom, dropping the last ones
// if they don't fit.
func footer(c *Canvas, fonts *Fonts, bg, line color.RGBA, hints ...[2]string) {
	fy := c.H - 17
	c.Fill(0, fy-1, c.W, 1, line)
	c.Fill(0, fy, c.W, 17, bg)
	f := fonts.Body(10)
	x := 5
	for _, h := range hints {
		w := max(f.Width(h[0])+6, f.height) + 3 + f.Width(h[1])
		if x+w > c.W-3 {
			break
		}
		x = hint(c, f, x, fy+3, h[0], h[1], guideWhite, guideDim)
	}
}

// header: the navy title bar shared by the guide and the DVR.
func header(c *Canvas, fonts *Fonts, sub string, now time.Time, rec bool) {
	W := c.W
	for y := 0; y < c.H; y++ {
		c.Fill(0, y, W, 1, mix(rgb(0x06, 0x0c, 0x38), guideNavy, float64(y)/float64(c.H)))
	}
	c.Fill(0, 0, W, 30, rgb(0x04, 0x08, 0x2a))
	c.Fill(0, 29, W, 1, guideCyan)
	c.Text(fonts.Head(12), 6, 3, "RABBIT EARS", guideYellow)
	x := 6
	if rec {
		c.Circle(10, 22, 3, rgb(0xff, 0x30, 0x30), true, 0)
		x = 17
	}
	c.Text(fonts.Body(10), x, 16, sub, guideCyan)
	clock := now.Format("3:04")
	vt := fonts.VT(26)
	ap := fonts.VT(13)
	c.Text(vt, W-6-ap.Width("PM")-2-vt.Width(clock), 1, clock, guideWhite)
	c.Text(ap, W-6-ap.Width("PM"), 11, now.Format("PM"), guideWhite)
}

// ---------- channel guide ----------

func DrawGuide(c *Canvas, fonts *Fonts, chans []*Channel, g *Guide, now time.Time, cur int) {
	W := c.W
	header(c, fonts, "CHANNEL GUIDE", now, false)
	// column heads
	c.Fill(0, 30, W, 14, rgb(0x0e, 0x22, 0x80))
	hf := fonts.Body(10)
	c.Text(hf, 7, 31, "CH", guideDim)
	c.Text(hf, 44, 31, "ON NOW", guideYellow)
	if len(chans) > guideRows {
		c.TextRight(hf, W-6, 31, fmt.Sprintf("%d OF %d", g.Sel+1, len(chans)), guideDim)
	}

	if g.Sel < g.Top {
		g.Top = g.Sel
	}
	if g.Sel >= g.Top+guideRows {
		g.Top = g.Sel - guideRows + 1
	}
	rowH := 44
	y0 := 47
	numF := fonts.VT(24)
	nameF := fonts.Body(10)
	titleF := fonts.Body(13)
	metaF := fonts.VT(14)
	for r := 0; r < guideRows; r++ {
		i := g.Top + r
		if i >= len(chans) {
			break
		}
		ch := chans[i]
		y := y0 + r*rowH
		sel := i == g.Sel
		box := guideBlue
		if sel {
			box = guideBlue2
		}
		c.Fill(3, y, 36, rowH-3, box)
		c.TextCenter(numF, 21, y+5, fmt.Sprintf("%d", ch.Num), guideYellow)
		if i == cur {
			c.TextCenter(fonts.Body(8), 21, y+29, "TUNED", guideCyan)
		}
		x := 42
		w := W - 3 - x
		c.Fill(x, y, w, rowH-3, box)
		c.Text(nameF, x+5, y+1, fitText(nameF, strings.ToUpper(ch.Name), w-10), guideCyan)
		air := ch.Upcoming(now, 2)
		if len(air) > 0 {
			a := air[0]
			c.Text(titleF, x+5, y+11, fitText(titleF, a.Item.Label(), w-10), guideWhite)
			meta := fmtDur(a.End.Sub(now)) + " LEFT"
			if len(air) > 1 {
				meta += "  NEXT " + strings.ToUpper(air[1].Start.Format("3:04"))
			}
			c.Text(metaF, x+5, y+24, fitText(metaF, meta, w-10), guideDim)
			if span := a.End.Sub(a.Start).Seconds(); span > 0 {
				p := now.Sub(a.Start).Seconds() / span
				c.Fill(x+5, y+rowH-6, w-10, 2, rgb(0x0a, 0x1a, 0x60))
				c.Fill(x+5, y+rowH-6, int(float64(w-10)*clamp01(p)), 2, guideCyan)
			}
		}
		if sel {
			c.Border(1, y-2, W-2, rowH+1, 2, guideYellow)
		}
	}
	footer(c, fonts, rgb(0x04, 0x08, 0x2a), guideCyan, [2]string{"A", "WATCH"}, [2]string{"B", "BACK"}, [2]string{"START", "SETUP"})
}

// ---------- colour bars / messages ----------

func DrawBars(c *Canvas, fonts *Fonts, title, line1, line2 string) {
	W, H := c.W, c.H
	top := []color.RGBA{rgb(192, 192, 192), rgb(192, 192, 0), rgb(0, 192, 192), rgb(0, 192, 0), rgb(192, 0, 192), rgb(192, 0, 0), rgb(0, 0, 192)}
	mid := []color.RGBA{rgb(0, 0, 192), rgb(19, 19, 19), rgb(192, 0, 192), rgb(19, 19, 19), rgb(0, 192, 192), rgb(19, 19, 19), rgb(192, 192, 192)}
	h1 := H * 67 / 100
	h2 := H * 8 / 100
	for i := 0; i < 7; i++ {
		x0, x1 := i*W/7, (i+1)*W/7
		c.Fill(x0, 0, x1-x0, h1, top[i])
		c.Fill(x0, h1, x1-x0, h2, mid[i])
	}
	bot := []struct {
		w   int
		col color.RGBA
	}{{5, rgb(0, 33, 76)}, {5, rgb(255, 255, 255)}, {5, rgb(50, 0, 106)}, {5, rgb(19, 19, 19)}, {2, rgb(9, 9, 9)}, {1, rgb(19, 19, 19)}, {1, rgb(29, 29, 29)}, {4, rgb(19, 19, 19)}}
	x := 0
	for i, b := range bot {
		w := b.w * W / 28
		if i == len(bot)-1 {
			w = W - x
		}
		c.Fill(x, h1+h2, w, H-h1-h2, b.col)
		x += w
	}
	if title == "" {
		return
	}
	f := fonts.Body(11)
	lines := f.Wrap(strings.TrimSpace(line1+" "+line2), 196)
	bw, bh := 216, 44+len(lines)*15
	bx, by := (W-bw)/2, (H-bh)/2-14
	c.Fill(bx, by, bw, bh, rgb(0, 0, 0))
	c.Border(bx+3, by+3, bw-6, bh-6, 1, rgb(0xe8, 0xe8, 0xe8))
	c.TextCenter(fonts.VT(28), W/2, by+7, title, rgb(0xf4, 0xf4, 0xf4))
	for i, ln := range lines {
		c.TextCenter(f, W/2, by+36+i*15, ln, rgb(0xc8, 0xc8, 0xc8))
	}
}

// DrawNoSignal: snow with a box explaining why the channel won't play.
func DrawNoSignal(c *Canvas, fonts *Fonts, snow *Snow, ch *Channel, why string) {
	snow.Draw(c)
	f := fonts.Body(10)
	lines := f.Wrap(why, 204)
	if len(lines) > 3 {
		lines = lines[:3]
	}
	bw, bh := 224, 84+len(lines)*13
	bx, by := (c.W-bw)/2, (c.H-bh)/2
	c.Blend(bx, by, bw, bh, colBlack, 0.82)
	c.TextCenter(fonts.VT(28), c.W/2, by+6, "NO SIGNAL", rgb(0xf0, 0xf0, 0xf0))
	if ch != nil {
		vt := fonts.VT(16)
		c.TextCenter(vt, c.W/2, by+36, fitText(vt, fmt.Sprintf("CH %d  %s", ch.Num, strings.ToUpper(ch.Name)), bw-16), osdGreen)
	}
	for i, ln := range lines {
		c.TextCenter(f, c.W/2, by+58+i*13, ln, rgb(0xc0, 0xc0, 0xc0))
	}
	c.TextCenter(f, c.W/2, by+bh-18, "UP/DOWN: CHANGE CHANNEL", rgb(0x90, 0x90, 0x90))
}

// ---------- SETUP ----------

func DrawMenu(c *Canvas, fonts *Fonts, m *Menu) {
	bg := rgb(0x10, 0x1c, 0xa8)
	white := rgb(0xf4, 0xf6, 0xff)
	dim := rgb(0x7a, 0x86, 0xd0)
	cyan := rgb(0x6c, 0xe4, 0xff)
	c.Fill(0, 0, c.W, c.H, bg)
	c.Scanlines(0, 0, c.W, c.H, 0.10)

	c.Text(fonts.VT(30), 8, 0, "SETUP", white)
	c.TextRight(fonts.VT(14), c.W-8, 12, "RABBIT EARS "+version, dim)
	c.Fill(8, 29, c.W-16, 2, white)

	row := fonts.VT(18)
	y := 34
	h := 19
	for r := 0; r < menuRows; r++ {
		lab, val := m.label(r)
		col := white
		if !m.enabled(r) {
			col = dim
		}
		if r == rowOff {
			y += 3
		}
		if r == m.Sel {
			c.Fill(4, y, c.W-8, h, cyan)
			col = bg
		}
		c.Text(row, 10, y+1, lab, col)
		if val != "" {
			vw := row.Width(val)
			x := c.W - 20 - vw
			c.Text(row, x, y+1, val, col)
			if r == m.Sel {
				arrow(c, x-10, y+h/2, -1, col)
				arrow(c, c.W-15, y+h/2, 1, col)
			}
		}
		y += h
	}

	hy := y + 6
	c.Fill(8, hy-4, c.W-16, 1, dim)
	hf := fonts.Body(10)
	for i, ln := range hf.Wrap(strings.ToUpper(m.help()), c.W-16) {
		if hy+i*12 > c.H-12 {
			break
		}
		c.Text(hf, 8, hy+i*12, ln, white)
	}
}

// arrow draws a small solid triangle pointing left (d<0) or right.
func arrow(c *Canvas, x, cy, d int, col color.RGBA) {
	for i := 0; i < 5; i++ {
		h := 5 - i
		xx := x + i
		if d < 0 {
			xx = x + 4 - i
		}
		c.Fill(xx, cy-h, 1, h*2, col)
	}
}

// ---------- the power button ----------

var powerChoices = []string{"RESUME", "TURN TV OFF", "SHUT DOWN"}

const (
	powerResume = iota
	powerTVOff
	powerShutdown
)

// DrawPowerMenu: the little box the power button brings up, over a dimmed
// copy of what was on screen.
func DrawPowerMenu(c *Canvas, fonts *Fonts, under *Canvas, sel int) {
	copy(c.Pix, under.Pix)
	c.Blend(0, 0, c.W, c.H, colBlack, 0.55)
	bg := rgb(0x10, 0x1c, 0xa8)
	white := rgb(0xf4, 0xf6, 0xff)
	cyan := rgb(0x6c, 0xe4, 0xff)
	bw, bh := 160, 34+len(powerChoices)*24
	bx, by := (c.W-bw)/2, (c.H-bh)/2
	c.Fill(bx, by, bw, bh, bg)
	c.Border(bx, by, bw, bh, 2, white)
	c.TextCenter(fonts.VT(20), c.W/2, by+4, "POWER", white)
	c.Fill(bx+8, by+25, bw-16, 1, white)
	f := fonts.VT(18)
	for i, s := range powerChoices {
		y := by + 30 + i*24
		col := white
		if i == sel {
			c.Fill(bx+6, y, bw-12, 21, cyan)
			col = bg
		}
		c.TextCenter(f, c.W/2, y+2, s, col)
	}
}

// ---------- DVR ----------

func DrawDVR(c *Canvas, fonts *Fonts, d *DVR, now time.Time) {
	W := c.W
	header(c, fonts, "DVR · ON DEMAND", now, true)
	var crumbs []string
	for _, l := range d.stack[1:] {
		crumbs = append(crumbs, strings.ToUpper(l.node.Name))
	}
	crumb := "ALL CHANNELS"
	if len(crumbs) > 0 {
		crumb = strings.Join(crumbs, " › ")
	}
	c.Fill(0, 30, W, 14, rgb(0x0e, 0x22, 0x80))
	cf := fonts.Body(10)
	l := d.top()
	right := ""
	if n := len(l.node.Kids); n > dvrRows {
		right = fmt.Sprintf("%d OF %d", l.sel+1, n)
	}
	// show the end of a long path: that's where you are
	room := W - 14 - cf.Width(right) - 6
	if cf.Width(crumb) > room {
		r := []rune(crumb)
		for len(r) > 1 && cf.Width("…"+string(r)) > room {
			r = r[1:]
		}
		crumb = "…" + string(r)
	}
	c.Text(cf, 7, 31, crumb, guideYellow)
	if right != "" {
		c.TextRight(cf, W-6, 31, right, guideDim)
	}

	rowH := 34
	y0 := 47
	titleF := fonts.Body(13)
	metaF := fonts.VT(14)
	for r := 0; r < dvrRows; r++ {
		i := l.top + r
		if i >= len(l.node.Kids) {
			break
		}
		n := l.node.Kids[i]
		y := y0 + r*rowH
		sel := i == l.sel
		box := guideBlue
		if sel {
			box = guideBlue2
		}
		c.Fill(3, y, W-6, rowH-3, box)
		x := 8
		if n.isFolder() {
			eps := n.episodes(nil)
			fresh := 0
			for _, it := range eps {
				if !d.mark(it).Done {
					fresh++
				}
			}
			drawTapeSmall(c, x, y+9, fresh > 0)
			x += 22
			meta := fmt.Sprintf("%d EPS", len(eps))
			if len(eps) == 1 {
				meta = "1 EP"
			}
			if fresh > 0 && fresh < len(eps) {
				meta = fmt.Sprintf("%d NEW", fresh)
			} else if fresh == 0 {
				meta = "WATCHED"
			}
			c.Text(titleF, x, y+2, fitText(titleF, strings.ToUpper(n.Name), W-x-10), guideWhite)
			c.Text(metaF, x, y+16, meta, guideDim)
		} else {
			it := n.Item
			m := d.mark(it)
			col := guideWhite
			switch {
			case m.Done:
				col = guideDim
				drawCheckSmall(c, x+1, y+8, guideDim)
			case m.Pos > 0:
				c.Circle(x+6, y+14, 5, guideYellow, false, 2)
			default:
				c.Circle(x+6, y+14, 5, guideCyan, true, 0)
			}
			x += 22
			meta := ""
			if it.Dur > 0 {
				meta = fmtDur(time.Duration(it.Dur * float64(time.Second)))
				if !m.Done && m.Pos > 0 {
					meta = fmtDur(time.Duration((it.Dur-m.Pos)*float64(time.Second))) + " LEFT"
				}
			}
			c.Text(titleF, x, y+2, fitText(titleF, n.Name, W-x-10), col)
			mw := 0
			if meta != "" {
				mw = metaF.Width(meta)
				c.Text(metaF, x, y+16, meta, guideDim)
			}
			if !m.Done && m.Pos > 0 && it.Dur > 0 {
				bx := x + mw + 8
				bw := W - 12 - bx
				c.Fill(bx, y+21, bw, 3, rgb(0x0a, 0x1a, 0x60))
				c.Fill(bx, y+21, int(float64(bw)*clamp01(m.Pos/it.Dur)), 3, guideYellow)
			}
		}
		if sel {
			c.Border(1, y-2, W-2, rowH+1, 2, guideYellow)
		}
	}
	if len(l.node.Kids) == 0 {
		c.TextCenter(fonts.Body(12), W/2, 120, "NOTHING IN HERE YET", guideDim)
	}

	if d.message != "" {
		fy := c.H - 17
		c.Fill(0, fy-1, W, 1, guideCyan)
		c.Fill(0, fy, W, 17, rgb(0x04, 0x08, 0x2a))
		f := fonts.Body(10)
		c.Text(f, 5, fy+3, fitText(f, strings.ToUpper(d.message), W-10), guideYellow)
		return
	}
	hints := [][2]string{}
	if s := d.selected(); s != nil && s.isFolder() {
		hints = append(hints, [2]string{"A", "OPEN"})
	} else {
		hints = append(hints, [2]string{"A", "PLAY"}, [2]string{"Y", "RESTART"})
	}
	if len(d.stack) > 1 {
		hints = append(hints, [2]string{"B", "BACK"})
	}
	hints = append(hints, [2]string{"X", "SEEN"}, [2]string{"START", "SETUP"})
	footer(c, fonts, rgb(0x04, 0x08, 0x2a), guideCyan, hints...)
}

func drawTapeSmall(c *Canvas, x, y int, fresh bool) {
	c.Fill(x, y, 16, 11, rgb(0x14, 0x14, 0x18))
	lab := guideDim
	if fresh {
		lab = guideCyan
	}
	c.Fill(x+2, y+2, 12, 3, lab)
	c.Fill(x+3, y+7, 3, 2, rgb(0x60, 0x60, 0x68))
	c.Fill(x+10, y+7, 3, 2, rgb(0x60, 0x60, 0x68))
}

func drawCheckSmall(c *Canvas, x, y int, col color.RGBA) {
	for i := 0; i < 4; i++ {
		c.Fill(x+i, y+5+i, 2, 2, col)
	}
	for i := 0; i < 7; i++ {
		c.Fill(x+4+i, y+8-i, 2, 2, col)
	}
}

// DrawUpNext: the card between episodes.
func DrawUpNext(c *Canvas, fonts *Fonts, title string) {
	c.Fill(0, 0, c.W, c.H, colBlack)
	c.TextCenter(fonts.VT(24), c.W/2, 72, "UP NEXT", guideCyan)
	f := fonts.Body(14)
	for i, ln := range f.Wrap(title, c.W-24) {
		if i > 2 {
			break
		}
		c.TextCenter(f, c.W/2, 104+i*19, ln, guideWhite)
	}
	c.TextCenter(fonts.Body(10), c.W/2, 178, "B: BACK TO RECORDINGS", guideDim)
}

// ---------- the OSD, at panel size ----------

// videoRect is where tvplay puts a w×h video on a pw×ph panel (it does the
// same arithmetic).
func videoRect(pw, ph, w, h int, fill bool) image.Rectangle {
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	a := float64(w) / float64(h)
	sa := float64(pw) / float64(ph)
	if fill {
		return image.Rect(0, 0, pw, ph)
	}
	if a > sa {
		rh := int(float64(pw)/a+0.5) &^ 1
		y := (ph - rh) / 2
		return image.Rect(0, y, pw, y+rh)
	}
	rw := int(float64(ph)*a+0.5) &^ 1
	x := (pw - rw) / 2
	return image.Rect(x, 0, x+rw, ph)
}

// onPanel draws an OSD made for the picture rectangle into a panel-sized
// image at the rectangle's place.
func onPanel(pw, ph int, r image.Rectangle, osd *image.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, pw, ph))
	b := osd.Bounds()
	for y := 0; y < b.Dy() && r.Min.Y+y < ph; y++ {
		copy(out.Pix[(r.Min.Y+y)*out.Stride+r.Min.X*4:], osd.Pix[y*osd.Stride:y*osd.Stride+min(b.Dx(), pw-r.Min.X)*4])
	}
	return out
}

// osdScale picks the OSD's size for a picture h pixels tall: the Miyoo
// layout scaled down, but never so small the numbers stop reading.
func osdScale(h int) float64 { return math.Max(float64(h)/240, 0.8) }
