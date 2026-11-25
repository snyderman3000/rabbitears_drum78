package main

// Extra framebuffer helpers: reading back what tvplay left on screen (for
// the CRT power-off), and scaling a canvas of another size onto the panel.

// Reset is a no-op on the Nano: fbtft has a single page and tvplay draws
// straight into it.
func (fb *Framebuffer) Reset() {}

// Capture copies what is on screen right now into the canvas.
func (fb *Framebuffer) Capture(c *Canvas) bool {
	w, h := fb.W, fb.H
	if c.W < w || c.H < h {
		return false
	}
	for y := 0; y < h; y++ {
		src := fb.mem[y*fb.stride:]
		dst := c.Pix[y*c.Stride:]
		for x := 0; x < w; x++ {
			d := x * 4
			if fb.bpp == 16 {
				p := uint16(src[x*2]) | uint16(src[x*2+1])<<8
				r, g, b := byte(p>>11&31), byte(p>>5&63), byte(p&31)
				dst[d], dst[d+1], dst[d+2], dst[d+3] = r<<3|r>>2, g<<2|g>>4, b<<3|b>>2, 255
			} else {
				dst[d], dst[d+1], dst[d+2], dst[d+3] = src[d+2], src[d+1], src[d], 255
			}
		}
	}
	return true
}

// PresentFit shows a canvas of any size on the panel, scaling it if needed.
func (fb *Framebuffer) PresentFit(c *Canvas) {
	if c.W == fb.W && c.H == fb.H {
		fb.Present(c)
		return
	}
	if fb.scaled == nil || fb.scaled.W != fb.W || fb.scaled.H != fb.H {
		fb.scaled = NewCanvas(fb.W, fb.H)
	}
	scaleInto(fb.scaled, c)
	fb.Present(fb.scaled)
}

// CaptureFit reads the screen into a canvas of any size.
func (fb *Framebuffer) CaptureFit(c *Canvas) bool {
	if c.W == fb.W && c.H == fb.H {
		return fb.Capture(c)
	}
	full := NewCanvas(fb.W, fb.H)
	if !fb.Capture(full) {
		return false
	}
	scaleInto(c, full)
	return true
}

// scaleInto resizes src onto dst (bilinear).
func scaleInto(dst, src *Canvas) {
	sx := float64(src.W-1) / float64(max(dst.W-1, 1))
	sy := float64(src.H-1) / float64(max(dst.H-1, 1))
	for y := 0; y < dst.H; y++ {
		fy := float64(y) * sy
		y0 := int(fy)
		y1 := min(y0+1, src.H-1)
		wy := fy - float64(y0)
		r0 := src.Pix[y0*src.Stride:]
		r1 := src.Pix[y1*src.Stride:]
		d := dst.Pix[y*dst.Stride:]
		for x := 0; x < dst.W; x++ {
			fx := float64(x) * sx
			x0 := int(fx)
			x1 := min(x0+1, src.W-1)
			wx := fx - float64(x0)
			for k := 0; k < 3; k++ {
				a := float64(r0[x0*4+k])*(1-wx) + float64(r0[x1*4+k])*wx
				b := float64(r1[x0*4+k])*(1-wx) + float64(r1[x1*4+k])*wx
				d[x*4+k] = uint8(a*(1-wy) + b*wy + 0.5)
			}
			d[x*4+3] = 255
		}
	}
}
