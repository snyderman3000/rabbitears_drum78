package main

// Plays one video at a time with tvplay, the small FFmpeg-based player that
// ships in the OPK (DrUm78's OS has no video player of its own). tvplay
// draws straight to the panel; Rabbit Ears hands it:
//   * -ss / -t: where to start and when a commercial break is due,
//   * -osd: the channel number, drawn here at panel size into a raw RGBA
//     file and shown while the playing position is in [from, to],
//   * -dvr: on-demand mode, where tvplay itself seeks and pauses on the
//     d-pad and A, and writes where it got to into -pos.

import (
	"encoding/binary"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type exitEvent struct {
	gen     int
	started time.Time
	err     error
}

type Player struct {
	Bin     string
	Work    string
	Fill    bool // crop to fill the square screen instead of letterboxing
	cmd     *exec.Cmd
	gen     int
	Exited  chan exitEvent
	running bool
}

func NewPlayer(bin, work string) *Player {
	os.MkdirAll(work, 0o755)
	return &Player{Bin: bin, Work: work, Exited: make(chan exitEvent, 4)}
}

// PosFile is where tvplay reports the playing position in DVR mode.
func (p *Player) PosFile() string { return filepath.Join(p.Work, "pos.txt") }

// LogFile holds tvplay's complaints about the last video.
func (p *Player) LogFile() string { return filepath.Join(p.Work, "tvplay.log") }

// Position reads the last position tvplay reported (-1 if none).
func (p *Player) Position() float64 {
	b, err := os.ReadFile(p.PosFile())
	if err != nil {
		return -1
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return -1
	}
	return v
}

// writeOverlay saves an OSD image as tvplay's raw overlay: "RBOV", width and
// height (u16 LE), then RGBA rows.
func writeOverlay(path string, img *image.NRGBA) error {
	b := img.Bounds()
	out := make([]byte, 8, 8+b.Dx()*b.Dy()*4)
	copy(out, "RBOV")
	binary.LittleEndian.PutUint16(out[4:], uint16(b.Dx()))
	binary.LittleEndian.PutUint16(out[6:], uint16(b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		out = append(out, img.Pix[y*img.Stride:y*img.Stride+b.Dx()*4]...)
	}
	return os.WriteFile(path, out, 0o644)
}

// Play starts path at ss seconds. limit > 0 stops it after that many
// seconds (a commercial break is due); tvplay then exits by itself.
func (p *Player) Play(path string, ss, limit float64, osd string, osdFrom, osdTo float64, dvr bool) error {
	p.Stop()
	args := []string{}
	if ss > 0.05 {
		args = append(args, "-ss", fmt.Sprintf("%.2f", ss))
	}
	if limit > 0 {
		args = append(args, "-t", fmt.Sprintf("%.2f", limit))
	}
	if osd != "" {
		args = append(args, "-osd", osd, "-osd-from", fmt.Sprintf("%.2f", osdFrom), "-osd-to", fmt.Sprintf("%.2f", osdTo))
	}
	if p.Fill {
		args = append(args, "-fill")
	}
	if dvr {
		os.Remove(p.PosFile())
		args = append(args, "-dvr", "-pos", p.PosFile(), "-input", FindButtons())
	}
	if fb := os.Getenv("RABBITEARS_FB"); fb != "" {
		args = append(args, "-fb", fb)
	}
	args = append(args, path)
	cmd := exec.Command(p.Bin, args...)
	cmd.Dir = p.Work
	if lf, err := os.OpenFile(p.LogFile(), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		cmd.Stderr = lf
		defer lf.Close()
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.gen++
	p.cmd = cmd
	p.running = true
	gen, started := p.gen, time.Now()
	log.Printf("play %q from %.1fs for %.1fs (pid %d)", path, ss, limit, cmd.Process.Pid)
	go func() {
		err := cmd.Wait()
		p.Exited <- exitEvent{gen: gen, started: started, err: err}
	}()
	return nil
}

// Current reports whether an exit event belongs to the video playing now.
func (p *Player) Current(ev exitEvent) bool {
	if ev.gen == p.gen {
		p.running = false
		return true
	}
	return false
}

func (p *Player) Running() bool { return p.running }

// Stop ends playback: SIGTERM (tvplay writes its position and exits), then
// SIGKILL if it hangs.
func (p *Player) Stop() {
	if p.cmd == nil || !p.running {
		return
	}
	cmd := p.cmd
	p.gen++ // its exit event is now stale
	p.running = false
	pid := cmd.Process.Pid
	syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil && strings.Contains(string(b), ") Z ") {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	syscall.Kill(-pid, syscall.SIGKILL)
}
