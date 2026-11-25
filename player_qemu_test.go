package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Runs the real ARM tvplay (under qemu-arm) with the exact arguments the app
// builds, drawing into a file instead of /dev/fb0, then checks the picture
// and the channel number landed where they should.
//
//	RABBITEARS_TVPLAY=tvplay/out/tvplay RABBITEARS_TESTVIDEO=file.mp4 go test -run RealTvplay
//
// (file.mp4: 320x240, with a bright picture; vhsify output is ideal.)
func TestRealTvplay(t *testing.T) {
	bin, vid := os.Getenv("RABBITEARS_TVPLAY"), os.Getenv("RABBITEARS_TESTVIDEO")
	if bin == "" || vid == "" {
		t.Skip("needs RABBITEARS_TVPLAY and RABBITEARS_TESTVIDEO")
	}
	bin, _ = filepath.Abs(bin)
	work := t.TempDir()
	fb := filepath.Join(work, "fb.raw")
	t.Setenv("RABBITEARS_FB", fb)
	p := NewPlayer(bin, work)
	ch := &Channel{Num: 4, Name: "Nature Hour"}
	r := videoRect(240, 240, 320, 240, false)
	osd := filepath.Join(work, "osd.rgba")
	if err := writeOverlay(osd, onPanel(240, 240, r, RenderOSD(newFonts(), r.Dx(), r.Dy(), ch, nil, "", time.Now()))); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := p.Play(vid, 3, 2, osd, 0, 100, false); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-p.Exited:
		if ev.err != nil {
			b, _ := os.ReadFile(p.LogFile())
			t.Fatalf("tvplay failed: %v %s", ev.err, b)
		}
	case <-time.After(60 * time.Second):
		p.Stop()
		t.Fatal("tvplay hung")
	}
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Fatalf("a 2 s limit ended after %v", d)
	}
	b, err := os.ReadFile(fb)
	if err != nil || len(b) != 240*240*2 {
		t.Fatalf("framebuffer file: %v (%d bytes)", err, len(b))
	}
	px := func(x, y int) uint16 { o := (y*240 + x) * 2; return uint16(b[o]) | uint16(b[o+1])<<8 }
	if px(120, 5) != 0 || px(120, 235) != 0 {
		t.Fatal("letterbox bars aren't black")
	}
	lit := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := 0; x < 240; x++ {
			if px(x, y) != 0 {
				lit++
			}
		}
	}
	if lit < 240*180/2 {
		t.Fatalf("only %d pixels of picture", lit)
	}
	// the OSD's green channel number, top right of the picture
	green := 0
	for y := r.Min.Y; y < r.Min.Y+50; y++ {
		for x := 150; x < 240; x++ {
			if px(x, y) == 0x57e9 { // osdGreen in RGB565
				green++
			}
		}
	}
	if green < 50 {
		t.Fatalf("channel number missing (%d green pixels)", green)
	}
}
