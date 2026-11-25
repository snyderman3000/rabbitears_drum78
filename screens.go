package main

// Everything Rabbit Ears draws itself: snow, the CRT power on/off, the
// on-screen channel number (an overlay tvplay draws over the picture).
// The guide, SETUP, colour bars and NO SIGNAL are in screens_nano.go.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
	"time"
)

var (
	osdGreen  = rgb(0x52, 0xff, 0x4a)
	osdShadow = rgb(0x00, 0x18, 0x00)

	guideNavy   = rgb(0x0a, 0x16, 0x5c)
	guideBlue   = rgb(0x18, 0x34, 0x9c)
	guideBlue2  = rgb(0x22, 0x46, 0xc0)
	guideCyan   = rgb(0x6c, 0xe4, 0xff)
	guideYellow = rgb(0xff, 0xe0, 0x3a)
	guideWhite  = rgb(0xf6, 0xf6, 0xff)
	guideDim    = rgb(0x9a, 0xa8, 0xe0)
)

type Fonts struct {
	vt   map[int]*Font // VT323 by pixel size
	body map[int]*Font
	head map[int]*Font
}

func newFonts() *Fonts {
	return &Fonts{vt: map[int]*Font{}, body: map[int]*Font{}, head: map[int]*Font{}}
}

func (f *Fonts) VT(px int) *Font {
	if f.vt[px] == nil {
		f.vt[px] = loadFont(lcdTTF, float64(px))
	}
	return f.vt[px]
}

func (f *Fonts) Body(px int) *Font {
	if f.body[px] == nil {
		f.body[px] = loadFont(bodyTTF, float64(px))
	}
	return f.body[px]
}

func (f *Fonts) Head(px int) *Font {
	if f.head[px] == nil {
		f.head[px] = loadFont(titleTTF, float64(px))
	}
	return f.head[px]
}

// ---------- snow ----------

type Snow struct {
	s     uint32
	frame int
}

// Draw fills the canvas with analog snow: chunky grain, faint horizontal
// streaks and a slow brighter band rolling down, like an untuned set.
func (n *Snow) Draw(c *Canvas) {
	n.frame++
	if n.s == 0 {
		n.s = uint32(time.Now().UnixNano()) | 1
	}
	s := n.s
	band := float64(n.frame%90) / 90 * float64(c.H+160)
	for y := 0; y < c.H; y += 2 {
		// a few rows get a horizontal streak
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		streak := 0
		if s%23 == 0 {
			streak = int(s>>27) + 18
		}
		d := math.Abs(float64(y) - band + 80)
		lift := 0
		if d < 80 {
			lift = int((80 - d) / 80 * 38)
		}
		o := y * c.Stride
		o2 := o + c.Stride
		for x := 0; x < c.W; x += 2 {
			s ^= s << 13
			s ^= s >> 17
			s ^= s << 5
			v := int(s>>24)*3/4 + lift + streak
			if v > 255 {
				v = 255
			}
			b := v + 6
			if b > 255 {
				b = 255
			}
			for _, p := range [2]int{o + x*4, o2 + x*4} {
				if p+7 >= len(c.Pix) {
					continue
				}
				c.Pix[p], c.Pix[p+1], c.Pix[p+2], c.Pix[p+3] = uint8(v), uint8(v), uint8(b), 255
				c.Pix[p+4], c.Pix[p+5], c.Pix[p+6], c.Pix[p+7] = uint8(v), uint8(v), uint8(b), 255
			}
		}
	}
	n.s = s
}

// ---------- CRT power on / off ----------

// PowerOnFrame: a bright line opens up into the picture. t runs 0→1.
func PowerOnFrame(c *Canvas, under *Canvas, t float64) {
	c.Fill(0, 0, c.W, c.H, colBlack)
	cy := c.H / 2
	if t < 0.35 {
		k := t / 0.35
		w := int(float64(c.W) * easeOut(k))
		c.Fill((c.W-w)/2, cy-1, w, 2, rgb(0xd8, 0xe8, 0xff))
		c.Blend((c.W-w)/2, cy-4, w, 8, rgb(0x90, 0xb0, 0xff), 0.35*k)
		return
	}
	k := easeOut((t - 0.35) / 0.65)
	h := int(float64(c.H) * k)
	if h < 2 {
		h = 2
	}
	// squeeze the full picture into the opening band
	for y := 0; y < h; y++ {
		sy := y * c.H / h
		copy(c.Pix[(cy-h/2+y)*c.Stride:(cy-h/2+y+1)*c.Stride], under.Pix[sy*under.Stride:(sy+1)*under.Stride])
	}
	c.Blend(0, cy-h/2, c.W, h, rgb(0xff, 0xff, 0xff), 0.5*(1-k))
}

// PowerOffFrame: the picture collapses to a line, then a dot that fades.
func PowerOffFrame(c *Canvas, last *Canvas, t float64) {
	c.Fill(0, 0, c.W, c.H, colBlack)
	cx, cy := c.W/2, c.H/2
	switch {
	case t < 0.45:
		k := easeIn(t / 0.45)
		h := int(float64(c.H) * (1 - k))
		if h < 2 {
			h = 2
		}
		for y := 0; y < h; y++ {
			sy := y * c.H / h
			copy(c.Pix[(cy-h/2+y)*c.Stride:(cy-h/2+y+1)*c.Stride], last.Pix[sy*last.Stride:(sy+1)*last.Stride])
		}
		c.Blend(0, cy-h/2, c.W, h, rgb(0xff, 0xff, 0xff), 0.15+0.7*k)
	case t < 0.75:
		k := easeIn((t - 0.45) / 0.3)
		w := int(float64(c.W) * (1 - k))
		if w < 4 {
			w = 4
		}
		c.Blend(cx-w/2, cy-4, w, 8, rgb(0x90, 0xb0, 0xff), 0.3)
		c.Fill(cx-w/2, cy-1, w, 2, rgb(0xf0, 0xf6, 0xff))
	default:
		k := (t - 0.75) / 0.25
		r := int(5 * (1 - k))
		if r > 0 {
			c.Circle(cx, cy, r+3, mix(colBlack, rgb(0x70, 0x90, 0xff), 0.4*(1-k)), true, 0)
			c.Circle(cx, cy, r, mix(colBlack, rgb(0xff, 0xff, 0xff), 1-k*0.8), true, 0)
		}
	}
}

func easeOut(t float64) float64 { t = clamp01(t); return 1 - (1-t)*(1-t) }
func easeIn(t float64) float64  { t = clamp01(t); return t * t }
func clamp01(t float64) float64 { return math.Max(0, math.Min(1, t)) }

// ---------- on-screen display (overlay for tvplay) ----------

// drawOver composites text onto a transparent NRGBA image.
func drawOver(img *image.NRGBA, f *Font, x, y int, s string, col color.RGBA) int {
	base := y + f.ascent
	b := img.Bounds()
	for _, r := range s {
		g := f.glyph(r)
		if g.mask != nil {
			gx, gy := x+g.off.X, base+g.off.Y
			mb := g.mask.Bounds()
			for yy := 0; yy < mb.Dy(); yy++ {
				py := gy + yy
				if py < 0 || py >= b.Dy() {
					continue
				}
				for xx := 0; xx < mb.Dx(); xx++ {
					px := gx + xx
					if px < 0 || px >= b.Dx() {
						continue
					}
					a := int(g.mask.Pix[yy*g.mask.Stride+xx])
					if a < 96 { // crisp, TV-style edges
						continue
					}
					o := py*img.Stride + px*4
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = col.R, col.G, col.B, 255
				}
			}
		}
		x += g.advance
	}
	return x
}

func osdTextShadow(img *image.NRGBA, f *Font, x, y int, s string, col color.RGBA, sh int) {
	for _, d := range [][2]int{{sh, sh}, {sh, 0}, {0, sh}} {
		drawOver(img, f, x+d[0], y+d[1], s, osdShadow)
	}
	drawOver(img, f, x, y, s, col)
}

// fitText trims s with "..." until it fits in w pixels.
func fitText(f *Font, s string, w int) string {
	if f.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && f.Width(string(r)+"...") > w {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r)) + "..."
}

func osdWidth(f *Font, s string) int { return f.Width(s) }

// RenderOSD draws the channel number (top right) and, for the DISPLAY
// button, the programme and clock along the bottom, sized for a w×h video.
func RenderOSD(fonts *Fonts, w, h int, ch *Channel, info *Airing, note string, now time.Time) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	u := osdScale(h)
	px := func(v float64) int { return int(math.Round(v * u)) }
	sh := px(1.5)
	if sh < 1 {
		sh = 1
	}
	big := fonts.VT(px(46))
	small := fonts.VT(px(18))
	num := fmt.Sprintf("%d", ch.Num)
	right := w - px(16)
	top := px(10)
	nw := osdWidth(big, num)
	osdTextShadow(img, big, right-nw, top, num, osdGreen, sh)
	cw := osdWidth(small, "CH")
	osdTextShadow(img, small, right-nw-cw-px(4), top+px(8), "CH", osdGreen, sh)
	name := fitText(small, strings.ToUpper(ch.Name), w*3/4)
	osdTextShadow(img, small, right-osdWidth(small, name), top+px(44), name, osdGreen, sh)

	if info != nil {
		y := h - px(52)
		left := px(14)
		clock := strings.ToUpper(now.Format("3:04 PM"))
		osdTextShadow(img, small, left, y, clock, osdGreen, sh)
		left2 := fmt.Sprintf("%s LEFT", fmtDur(info.End.Sub(now)))
		title := fitText(small, strings.ToUpper(info.Item.Label()), w-left*2-osdWidth(small, left2)-px(12))
		if note != "" {
			osdTextShadow(img, small, left, y-px(18), fitText(small, note, w-left*2), osdGreen, sh)
		}
		osdTextShadow(img, small, left, y+px(18), title, osdGreen, sh)
		// a row of blocks showing how far into the programme we are
		span := info.End.Sub(info.Start).Seconds()
		done := 0.0
		if span > 0 {
			done = now.Sub(info.Start).Seconds() / span
		}
		bw, bh, n := px(6), px(5), 20
		for i := 0; i < n; i++ {
			x := right - (n-i)*(bw+px(2))
			col := rgb(0x1c, 0x5a, 0x1a)
			if float64(i)/float64(n) < done {
				col = osdGreen
			}
			for yy := 0; yy < bh; yy++ {
				for xx := 0; xx < bw; xx++ {
					o := (y+px(8)+yy)*img.Stride + (x+xx)*4
					if o >= 0 && o+3 < len(img.Pix) {
						img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = col.R, col.G, col.B, 255
					}
				}
			}
		}
		osdTextShadow(img, small, right-osdWidth(small, left2), y+px(18), left2, osdGreen, sh)
	}
	return img
}

// RenderPlayOSD: the VCR-style "PLAY" (or resume point) shown when a DVR
// episode starts.
func RenderPlayOSD(fonts *Fonts, w, h int, title string, from float64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	u := osdScale(h)
	px := func(v float64) int { return int(math.Round(v * u)) }
	sh := max(px(1.5), 1)
	big := fonts.VT(px(30))
	small := fonts.VT(px(18))
	label := "PLAY"
	if from > 0 {
		label = "PLAY  " + fmtClock(from)
	}
	x := px(14)
	// a solid "play" triangle
	tri := px(16)
	for i := 0; i < tri; i++ {
		hh := tri - i
		for yy := -hh / 2; yy <= hh/2; yy++ {
			for _, d := range [][2]int{{sh, sh}, {0, 0}} {
				col := osdShadow
				if d[0] == 0 {
					col = osdGreen
				}
				o := (px(14)+tri/2+yy+d[1])*img.Stride + (x+i+d[0])*4
				if o >= 0 && o+3 < len(img.Pix) {
					img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = col.R, col.G, col.B, 255
				}
			}
		}
	}
	osdTextShadow(img, big, x+tri+px(6), px(10), label, osdGreen, sh)
	osdTextShadow(img, small, x, h-px(30), fitText(small, strings.ToUpper(title), w-2*x), osdGreen, sh)
	return img
}

// fmtClock: 75 → "1:15", 3725 → "1:02:05".
func fmtClock(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	t := int(sec + 0.5)
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d.Minutes() + 0.5)
	if m >= 60 {
		return fmt.Sprintf("%d:%02d", m/60, m%60)
	}
	if m < 1 {
		return "<1 MIN"
	}
	return fmt.Sprintf("%d MIN", m)
}

func writePNG(path string, img image.Image) error {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ---------- channel guide ----------

type Guide struct {
	Sel, Top int
}

const guideRows = 4
