package main

// RG Nano framebuffer + evdev input (DrUm78 FunKey-OS).
//
// The panel is a 240x240 fbtft display: 16-bit RGB565, no panning, the
// right way up. The buttons come from fkgpiod, a uinput keyboard that maps
// each button to a letter key (/etc/fkgpiod.conf).

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	fbioGetVScreenInfo = 0x4600
	fbioPutVScreenInfo = 0x4601
	fbioGetFScreenInfo = 0x4602
)

type Framebuffer struct {
	f      *os.File
	mem    []byte
	W, H   int
	stride int
	bpp    int
	scaled *Canvas // PresentFit's buffer
}

func ioctl(fd uintptr, req uintptr, p unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(p))
	if e != 0 {
		return e
	}
	return nil
}

// OpenFramebuffer maps /dev/fb0. RABBITEARS_FB names a plain file instead
// (240x240 RGB565), for running on a PC.
func OpenFramebuffer() (*Framebuffer, error) {
	dev := "/dev/fb0"
	if d := os.Getenv("RABBITEARS_FB"); d != "" {
		dev = d
	}
	f, err := os.OpenFile(dev, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	fb := &Framebuffer{f: f, W: 240, H: 240, bpp: 16}
	var v [40]uint32 // struct fb_var_screeninfo
	real := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&v[0])) == nil && v[0] > 0
	if real {
		fb.W, fb.H = int(v[0]), int(v[1])
		fb.bpp = int(v[6])
	}
	if fb.bpp != 32 {
		fb.bpp = 16
	}
	fb.stride = fb.W * fb.bpp / 8
	if real {
		var fix [96]byte // struct fb_fix_screeninfo
		if ioctl(f.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fix[0])) == nil {
			ul := int(unsafe.Sizeof(uintptr(0)))
			lloff := 16 + ul + 4*4 + 2*3
			lloff = (lloff + 3) &^ 3
			if ll := int(binary.LittleEndian.Uint32(fix[lloff:])); ll >= fb.stride {
				fb.stride = ll
			}
		}
	} else if st, err := f.Stat(); err == nil && st.Size() < int64(fb.stride*fb.H) {
		f.Truncate(int64(fb.stride * fb.H))
	}
	mem, err := syscall.Mmap(int(f.Fd()), 0, fb.stride*fb.H, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, err
	}
	fb.mem = mem
	return fb, nil
}

// Present copies the canvas (RGBA) to the panel.
func (fb *Framebuffer) Present(c *Canvas) {
	w, h := min(fb.W, c.W), min(fb.H, c.H)
	for y := 0; y < h; y++ {
		src := c.Pix[y*c.Stride:]
		dst := fb.mem[y*fb.stride:]
		if fb.bpp == 16 {
			for x := 0; x < w; x++ {
				s := x * 4
				p := uint16(src[s])>>3<<11 | uint16(src[s+1])>>2<<5 | uint16(src[s+2])>>3
				dst[x*2], dst[x*2+1] = byte(p), byte(p>>8)
			}
		} else {
			for x := 0; x < w; x++ {
				d := x * 4
				dst[d], dst[d+1], dst[d+2], dst[d+3] = src[d+2], src[d+1], src[d], 0xff
			}
		}
	}
}

func (fb *Framebuffer) Clear() {
	for i := range fb.mem {
		fb.mem[i] = 0
	}
}

func (fb *Framebuffer) Close() {
	if fb.mem != nil {
		syscall.Munmap(fb.mem)
	}
	fb.f.Close()
}

// fkgpiod's key map (/etc/fkgpiod.conf) → Linux key codes.
const (
	BtnA      = 30 // KEY_A
	BtnB      = 48 // KEY_B
	BtnX      = 45 // KEY_X
	BtnY      = 21 // KEY_Y
	BtnUp     = 22 // KEY_U
	BtnDown   = 32 // KEY_D
	BtnLeft   = 38 // KEY_L
	BtnRight  = 19 // KEY_R
	BtnL1     = 50 // KEY_M
	BtnR1     = 49 // KEY_N
	BtnStart  = 31 // KEY_S
	BtnSelect = 16 // KEY_Q: the MENU line
	BtnFn     = 37 // KEY_K: FN, also the volume/brightness modifier, so unused
	BtnL2     = 47 // KEY_V: FN+L
	BtnR2     = 24 // KEY_O: FN+R

	// BtnPower isn't a key: the power button reaches the foreground app as
	// SIGUSR1, which main() turns into this.
	BtnPower = 1000
	// BtnMenu is the Miyoo's MENU button; the Nano has no such key.
	BtnMenu = -1

	// keyRepeat marks an auto-repeat of a held button
	keyRepeat = 1 << 16
)

var buttonsName = regexp.MustCompile(`Name="(uinput-s)?ample"`)

// FindButtons returns fkgpiod's input device. It registers as "ample" (its
// name is truncated); match on that, since a USB DAC can shift the numbers.
func FindButtons() string {
	if d := os.Getenv("RABBITEARS_INPUT"); d != "" {
		return d
	}
	f, err := os.Open("/proc/bus/input/devices")
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		match := false
		for sc.Scan() {
			ln := sc.Text()
			if strings.HasPrefix(ln, "N: Name=") {
				match = buttonsName.MatchString(ln)
			}
			if match && strings.HasPrefix(ln, "H: Handlers=") {
				for _, w := range strings.Fields(ln[12:]) {
					if strings.HasPrefix(w, "event") {
						return "/dev/input/" + w
					}
				}
			}
		}
	}
	return "/dev/input/event0"
}

// ReadInput streams key presses (and auto-repeat for the d-pad and shoulders).
func ReadInput(path string, out chan<- int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	send := func(code int) {
		select {
		case out <- code:
		default:
		}
	}
	// fkgpiod's uinput device may not auto-repeat, so held d-pad and
	// shoulder buttons repeat here unless the kernel turns out to do it.
	var mu sync.Mutex
	held := map[int]int{} // code → press generation
	gen := 0
	kernelRepeats := false
	softRepeat := func(code, g int) {
		time.Sleep(380 * time.Millisecond)
		for {
			mu.Lock()
			ok := held[code] == g && !kernelRepeats
			mu.Unlock()
			if !ok {
				return
			}
			send(code | keyRepeat)
			time.Sleep(110 * time.Millisecond)
		}
	}
	go func() {
		defer f.Close()
		// struct input_event from a 32-bit kernel: 16 bytes
		buf := make([]byte, 16)
		for {
			if _, err := io.ReadFull(f, buf); err != nil {
				return
			}
			typ := binary.LittleEndian.Uint16(buf[8:])
			code := int(binary.LittleEndian.Uint16(buf[10:]))
			val := int32(binary.LittleEndian.Uint32(buf[12:]))
			if typ != 1 {
				continue
			}
			repeatable := code == BtnUp || code == BtnDown || code == BtnLeft || code == BtnRight || code == BtnL1 || code == BtnR1
			switch val {
			case 1:
				send(code)
				if repeatable {
					mu.Lock()
					gen++
					held[code] = gen
					g := gen
					mu.Unlock()
					go softRepeat(code, g)
				}
			case 2:
				mu.Lock()
				kernelRepeats = true
				mu.Unlock()
				if repeatable {
					send(code | keyRepeat)
				}
			case 0:
				mu.Lock()
				delete(held, code)
				mu.Unlock()
			}
		}
	}()
	return nil
}
