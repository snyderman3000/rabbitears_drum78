package main

// Rabbit Ears — a 90s TV set, ported to the Anbernic RG Nano running
// DrUm78's FunKey-OS. Videos in SD:/Media/TV become channels; flip through
// them with the d-pad.

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const version = "1.1.0-nano.1"

const (
	ModeLive = "live"
	ModeDVR  = "dvr"

	PictureFit  = "4:3"  // whole picture, black bars above and below
	PictureFill = "zoom" // fill the square screen, sides cut off
)

type Config struct {
	TVFolder     string  `json:"tv_folder"`      // where the channels live
	FirstChannel int     `json:"first_channel"`  // number of the first unnumbered channel
	Mode         string  `json:"mode"`           // "live" (always on air) or "dvr" (on demand)
	Order        string  `json:"show_order"`     // rotate, in order or shuffle
	Commercials  bool    `json:"commercials"`    // play commercial breaks (live TV)
	AdsEveryMin  float64 `json:"ads_every_min"`  // minutes between breaks inside a show; 0 = between shows only
	AdsPerBreak  int     `json:"ads_per_break"`  // commercials in each break
	StaticMS     int     `json:"static_ms"`      // how long the snow shows between channels
	OSDSeconds   float64 `json:"osd_seconds"`    // how long the channel number stays up
	Picture      string  `json:"picture"`        // "4:3" or "zoom"
	Player       string  `json:"player"`         // tvplay binary ("" = the one in the app)
	PowerOnLast  bool    `json:"power_on_last"`  // come back on the channel you left
	OldLive      *bool   `json:"live,omitempty"` // 1.0 setting, read once and replaced by mode
}

func defaultConfig() Config {
	return Config{
		TVFolder:     "/mnt/Media/TV",
		FirstChannel: 2,
		Mode:         ModeLive,
		Order:        OrderRotate,
		Commercials:  true,
		AdsEveryMin:  8,
		AdsPerBreak:  3,
		StaticMS:     350,
		OSDSeconds:   3,
		Picture:      PictureFit,
		PowerOnLast:  true,
	}
}

func (c *Config) tidy() {
	if c.OldLive != nil {
		if !*c.OldLive {
			c.Mode = ModeDVR
		}
		c.OldLive = nil
	}
	if c.Mode != ModeDVR {
		c.Mode = ModeLive
	}
	if c.Picture != PictureFill {
		c.Picture = PictureFit
	}
	if o := parseOrder(c.Order); o != "" {
		c.Order = o
	} else {
		c.Order = OrderRotate
	}
	if c.FirstChannel < 1 {
		c.FirstChannel = 2
	}
	if c.StaticMS < 0 {
		c.StaticMS = 0
	}
	if c.OSDSeconds <= 0 {
		c.OSDSeconds = 3
	}
	if c.AdsEveryMin < 0 {
		c.AdsEveryMin = 0
	}
	if c.AdsEveryMin > 0 && c.AdsEveryMin < 3 {
		c.AdsEveryMin = 3
	}
	if c.AdsPerBreak < 1 {
		c.AdsPerBreak = 1
	}
	if c.AdsPerBreak > 10 {
		c.AdsPerBreak = 10
	}
}

func (c *Config) adOptions() AdOptions {
	return AdOptions{On: c.Commercials, EveryMin: c.AdsEveryMin, PerBreak: c.AdsPerBreak}
}

func saveConfig(path string, cfg Config) {
	if out, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		os.WriteFile(path, append(out, '\n'), 0o644)
	}
}

func loadConfig(path string) (Config, string) {
	cfg := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			saveConfig(path, cfg)
		}
		return cfg, ""
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return defaultConfig(), fmt.Sprintf("config.json: %v", err)
	}
	upgraded := cfg.OldLive != nil || !strings.Contains(string(b), `"mode"`)
	cfg.tidy()
	if upgraded {
		saveConfig(path, cfg) // add the 1.1 settings so they can be edited
	}
	return cfg, ""
}

type State struct {
	Channel string `json:"channel"` // key of the last channel watched
}

func main() {
	shot := flag.String("shot", "", "render a demo screen to shot.png: osd|info|ad|play|zoom|guide|menu|dvr|dvr2|upnext|power|bars|nosignal|snow|on|off")
	icon := flag.String("icon", "", "write the app icon PNG to this path")
	iconSize := flag.Int("icon-size", 32, "icon size in pixels")
	scan := flag.String("scan", "", "print the lineup for a TV folder and exit")
	flag.Parse()

	if *icon != "" {
		if err := writePNG(*icon, appIcon(*iconSize)); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}
	if *scan != "" {
		printLineup(*scan)
		return
	}
	if *shot != "" {
		if err := renderShot(*shot); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}

	exe, _ := os.Executable()
	appDir := filepath.Dir(exe)
	// the OPK is mounted read-only: settings and logs live on the card
	dataDir := os.Getenv("RABBITEARS_DATA")
	if dataDir == "" {
		dataDir = "/mnt/FunKey/RabbitEars"
	}
	if os.MkdirAll(dataDir, 0o755) != nil {
		dataDir = filepath.Join(os.TempDir(), "rabbitears-data")
		os.MkdirAll(dataDir, 0o755)
	}
	if lf, err := os.OpenFile(filepath.Join(dataDir, "rabbitears.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		log.SetOutput(lf)
	}
	log.Printf("Rabbit Ears %s", version)
	cfg, problem := loadConfig(filepath.Join(dataDir, "config.json"))
	player := cfg.Player
	if player == "" {
		player = filepath.Join(appDir, "tvplay")
	}

	fb, err := OpenFramebuffer()
	if err != nil {
		log.Fatalf("framebuffer: %v", err)
	}
	defer fb.Close()
	log.Printf("screen %dx%d", fb.W, fb.H)

	tv := &TV{
		cfg:     cfg,
		dataDir: dataDir,
		fb:      fb,
		canvas:  NewCanvas(fb.W, fb.H), // drawn at the panel's own size
		fonts:   newFonts(),
		keys:    make(chan int, 16),
		player:  NewPlayer(player, "/tmp/rabbitears"),
		problem: problem,
		dvr:     newDVR(filepath.Join(dataDir, "dvr.json")),
	}
	tv.player.Fill = cfg.Picture == PictureFill
	// The power button: FunKey-OS sends the foreground app SIGUSR1 and
	// powers off 0.1 s later unless the app cancels that and offers its own
	// menu, which is what GMenu2X and the emulators do.
	power := make(chan os.Signal, 2)
	signal.Notify(power, syscall.SIGUSR1)
	go func() {
		for range power {
			cancelPowerdown()
			select {
			case tv.keys <- BtnPower:
			default:
			}
		}
	}()
	input := FindButtons()
	log.Printf("buttons: %s", input)
	if err := ReadInput(input, tv.keys); err != nil {
		log.Fatalf("input: %v", err)
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-sig
		tv.player.Stop()
		if tv.mode == modeDVRPlay {
			tv.dvr.stopPlaying(false, tv.player.Position())
		}
		os.Exit(0)
	}()
	tv.Run()
}

func printLineup(dir string) {
	cfg := defaultConfig()
	chans := Scan(dir, ScanOptions{First: cfg.FirstChannel, Order: cfg.Order}, &durCache{m: map[string]cacheEntry{}})
	if len(chans) == 0 {
		fmt.Println("no channels found in", dir)
		return
	}
	now := time.Now()
	for _, c := range chans {
		c.Build(cfg.adOptions())
		si, off := c.At(now)
		seg := c.Segs[si]
		what := seg.Item.Label()
		if seg.Ad {
			what = "commercial: " + seg.Item.Title
		}
		fmt.Printf("CH %-3d %-24s %3d video(s), %d commercials, %s  now: %s @ %s", c.Num, c.Name, len(c.Items), len(c.Ads), c.Order, what, time.Duration((seg.From+off)*float64(time.Second)).Round(time.Second))
		if seg.Item.Dur <= 0 {
			fmt.Print("  (length unknown — not an MP4?)")
		} else if seg.Item.H > 480 {
			fmt.Printf("  (%dx%d — run it through vhsify)", seg.Item.W, seg.Item.H)
		}
		fmt.Println()
		for k, a := range c.Upcoming(now, 4) {
			if k > 0 {
				fmt.Printf("         %s  %s\n", a.Start.Format("3:04"), a.Item.Label())
			}
		}
	}
}

// ---------- the TV ----------

type TV struct {
	cfg     Config
	dataDir string
	fb      *Framebuffer
	canvas  *Canvas
	fonts   *Fonts
	keys    chan int
	player  *Player
	problem string

	chans    []*Channel
	cur      int
	prev     int
	curSeg   int
	segLimit float64 // how long the playing segment should run
	snow     Snow
	fails    int
	lastTick time.Time
	mode     int
	guide    Guide
	menu     Menu
	dvr      *DVR
	why      string
	pending  []int

	// the power button's menu
	powerSel   int
	powerUnder *Canvas
	powerFrom  int   // the mode it was opened from
	powerNode  *Node // the DVR episode that was playing
}

const (
	modeWatch = iota
	modeGuide
	modeNoSignal
	modeEmpty
	modeMenu
	modeDVR
	modeDVRPlay
	modePower
)

func (tv *TV) present() {
	tv.fb.Reset()
	tv.fb.PresentFit(tv.canvas)
}

func (tv *TV) configPath() string { return filepath.Join(tv.dataDir, "config.json") }

func (tv *TV) rescan() {
	cache := loadCache(filepath.Join(tv.dataDir, "lengths.json"))
	keep := ""
	if tv.cur < len(tv.chans) {
		keep = tv.chans[tv.cur].Key
	}
	tv.chans = Scan(tv.cfg.TVFolder, ScanOptions{First: tv.cfg.FirstChannel, Order: tv.cfg.Order}, cache)
	for _, c := range tv.chans {
		c.Build(tv.cfg.adOptions())
	}
	tv.cur, tv.prev = 0, 0
	for i, c := range tv.chans {
		if c.Key == keep {
			tv.cur, tv.prev = i, i
		}
	}
	tv.dvr.setLineup(tv.cfg.TVFolder, tv.chans)
	ads := 0
	if len(tv.chans) > 0 {
		ads = len(tv.chans[0].Ads)
	}
	log.Printf("%d channels in %s (order %s, commercials %v, %d in the shared pool)", len(tv.chans), tv.cfg.TVFolder, tv.cfg.Order, tv.cfg.Commercials, ads)
}

// adsFound counts the distinct commercials on the card (for the menu).
func (tv *TV) adsFound() int {
	seen := map[string]bool{}
	for _, c := range tv.chans {
		for _, a := range c.Ads {
			seen[a.Path] = true
		}
	}
	return len(seen)
}

func (tv *TV) Run() {
	os.MkdirAll(tv.cfg.TVFolder, 0o755)

	tv.rescan()
	if tv.cfg.PowerOnLast {
		var st State
		if b, err := os.ReadFile(filepath.Join(tv.dataDir, "state.json")); err == nil && json.Unmarshal(b, &st) == nil {
			for i, c := range tv.chans {
				if c.Key == st.Channel {
					tv.cur, tv.prev = i, i
				}
			}
		}
	}

	tv.powerOn()
	if _, err := os.Stat(tv.player.Bin); err != nil {
		tv.showEmpty("PLAYER MISSING", "tvplay wasn't found at", tv.player.Bin)
	} else if tv.problem != "" {
		tv.showEmpty("CONFIG ERROR", tv.problem, "Fix FunKey/RabbitEars/config.json")
	} else {
		tv.home(true)
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	tv.lastTick = time.Now()
	for {
		if len(tv.pending) > 0 {
			k := tv.pending[0]
			tv.pending = tv.pending[1:]
			if tv.handleKey(k) {
				tv.powerOff()
				return
			}
			continue
		}
		select {
		case k := <-tv.keys:
			if tv.handleKey(k) {
				tv.powerOff()
				return
			}
		case ev := <-tv.player.Exited:
			if !tv.player.Current(ev) {
				continue
			}
			if tv.mode == modeDVRPlay {
				tv.onDVREnded(ev, false)
				continue
			}
			tv.onSegmentEnded(ev)
		case now := <-tick.C:
			gap := now.Sub(tv.lastTick)
			tv.lastTick = now
			if gap > 4*time.Second {
				// the whole app was paused (the console was suspended)
				log.Printf("woke after %v", gap.Round(time.Second))
				if tv.mode == modeWatch {
					tv.tune(tv.cur, true, false) // back on schedule
				}
			}
			switch tv.mode {
			case modeGuide:
				tv.drawGuide()
			case modeDVR:
				tv.drawDVR()
			}
		}
	}
}

// home shows the starting screen of the current mode.
func (tv *TV) home(snow bool) {
	if len(tv.chans) == 0 {
		tv.showEmpty("NO CHANNELS", "Put videos (or folders of episodes) in Media/TV on the SD card,", "then press A.")
		return
	}
	if tv.cfg.Mode == ModeDVR {
		tv.openDVR()
		return
	}
	tv.tune(tv.cur, snow, false)
}

func (tv *TV) saveState() {
	if tv.cur < len(tv.chans) {
		if b, err := json.Marshal(State{Channel: tv.chans[tv.cur].Key}); err == nil {
			os.WriteFile(filepath.Join(tv.dataDir, "state.json"), b, 0o644)
		}
	}
}

// handleKey returns true when it's time to switch off.
func (tv *TV) handleKey(k int) bool {
	if k == BtnPower {
		if tv.mode == modePower {
			tv.closePower(powerResume) // a second press: back to the TV
			return false
		}
		tv.openPower()
		return false
	}
	if tv.mode == modeDVRPlay {
		// tvplay has the d-pad, A and L/R; B or SELECT stops
		if k == BtnB || k == BtnSelect {
			tv.player.Stop()
			tv.dvr.stopPlaying(false, tv.player.Position())
			tv.openDVR()
		}
		return false
	}
	k &^= keyRepeat
	switch tv.mode {
	case modePower:
		switch k {
		case BtnUp:
			tv.powerSel = (tv.powerSel + len(powerChoices) - 1) % len(powerChoices)
		case BtnDown:
			tv.powerSel = (tv.powerSel + 1) % len(powerChoices)
		case BtnA:
			return tv.closePower(tv.powerSel)
		case BtnB:
			return tv.closePower(powerResume)
		}
		if tv.mode == modePower {
			DrawPowerMenu(tv.canvas, tv.fonts, tv.powerUnder, tv.powerSel)
			tv.present()
		}
	case modeEmpty:
		if k == BtnA || k == BtnStart {
			tv.rescan()
			if len(tv.chans) > 0 && tv.problem == "" {
				tv.home(true)
			}
		}
	case modeMenu:
		if tv.menu.key(k) {
			if tv.menu.quit {
				return true
			}
			tv.closeMenu()
			return false
		}
		tv.drawMenu()
	case modeGuide:
		switch k {
		case BtnUp:
			tv.guide.Sel = (tv.guide.Sel - 1 + len(tv.chans)) % len(tv.chans)
		case BtnDown:
			tv.guide.Sel = (tv.guide.Sel + 1) % len(tv.chans)
		case BtnL1, BtnLeft:
			tv.guide.Sel = max(0, tv.guide.Sel-guideRows)
		case BtnR1, BtnRight:
			tv.guide.Sel = min(len(tv.chans)-1, tv.guide.Sel+guideRows)
		case BtnA:
			tv.switchTo(tv.guide.Sel)
			return false
		case BtnB, BtnSelect, BtnX:
			tv.tune(tv.cur, true, false)
			return false
		case BtnStart:
			tv.openMenu()
			return false
		}
		tv.drawGuide()
	case modeDVR:
		switch tv.dvr.browserKey(k) {
		case dvrPlay:
			tv.playDVR(tv.dvr.selected(), false)
		case dvrPlayFromStart:
			tv.playDVR(tv.dvr.selected(), true)
		case dvrLive:
			tv.cfg.Mode = ModeLive
			saveConfig(tv.configPath(), tv.cfg)
			tv.tune(tv.cur, true, false)
		case dvrMenu:
			tv.openMenu()
		default:
			tv.drawDVR()
		}
	default: // watching or no signal
		switch k {
		case BtnUp, BtnR1, BtnRight, BtnDown, BtnL1, BtnLeft:
			// fold a held or mashed button into one jump so surfing stays snappy
			n := len(tv.chans)
			step := tv.collectSteps(chanStep(k))
			tv.switchTo(((tv.cur+step)%n + n) % n)
		case BtnB:
			tv.switchTo(tv.prev) // "recall": the last channel you were on
		case BtnSelect, BtnX:
			tv.openGuide()
		case BtnStart:
			tv.openMenu()
		case BtnA, BtnY:
			tv.tune(tv.cur, false, true) // DISPLAY
		}
	}
	return false
}

func chanStep(k int) int {
	switch k {
	case BtnUp, BtnR1, BtnRight:
		return 1
	case BtnDown, BtnL1, BtnLeft:
		return -1
	}
	return 0
}

// collectSteps adds up channel presses already waiting in the queue; the
// first other button stops it and is handled afterwards.
func (tv *TV) collectSteps(step int) int {
	for {
		select {
		case k := <-tv.keys:
			if s := chanStep(k &^ keyRepeat); s != 0 {
				step += s
				continue
			}
			tv.pending = append(tv.pending, k)
		default:
		}
		return step
	}
}

func (tv *TV) switchTo(i int) {
	if len(tv.chans) == 0 {
		return
	}
	if i != tv.cur {
		tv.prev = tv.cur
	}
	tv.cur = i
	tv.fails = 0
	tv.tune(i, true, false)
	tv.saveState()
}

// tune starts channel i at whatever is on the air now.
func (tv *TV) tune(i int, withSnow, withInfo bool) {
	if len(tv.chans) == 0 {
		return
	}
	tv.player.Stop()
	tv.mode = modeWatch
	ch := tv.chans[i]
	si, off := ch.At(time.Now())
	// too close to the end to be worth starting: go to what's next
	if s := ch.Segs[si]; !s.Open && s.Len-off < 2 {
		si, off = (si+1)%len(ch.Segs), 0
	}
	tv.start(ch, si, off, withSnow, true, withInfo)
}

// start plays part of segment si from off seconds into it, showing snow
// (while tvplay loads) and the channel number.
func (tv *TV) start(ch *Channel, si int, off float64, withSnow, withOSD, withInfo bool) {
	tv.curSeg = si
	seg := ch.Segs[si]
	it := seg.Item
	ss := seg.From + off
	limit := 0.0
	if !seg.Open && !seg.Ad && seg.From+seg.Len < it.Dur-0.5 {
		limit = seg.Len - off // a break is due before the file ends
	}
	tv.segLimit = limit
	if limit == 0 && !seg.Open {
		tv.segLimit = seg.Len - off
	}
	osd := ""
	if withOSD {
		w, h := it.W, it.H
		if w <= 0 || h <= 0 {
			w, h = 320, 240
		}
		var info *Airing
		note := ""
		if withInfo {
			if air := ch.Upcoming(time.Now(), 1); len(air) > 0 {
				info = &air[0]
			}
			if seg.Ad {
				note = "COMMERCIAL BREAK  BACK IN " + fmtClock(ch.AdBreakLeft(si, off))
			}
		}
		r := videoRect(tv.fb.W, tv.fb.H, w, h, tv.player.Fill)
		img := onPanel(tv.fb.W, tv.fb.H, r, RenderOSD(tv.fonts, r.Dx(), r.Dy(), ch, info, note, time.Now()))
		osd = filepath.Join(tv.player.Work, fmt.Sprintf("osd%d.rgba", time.Now().UnixNano()%2))
		if err := writeOverlay(osd, img); err != nil {
			log.Printf("osd: %v", err)
			osd = ""
		}
	}
	secs := tv.cfg.OSDSeconds
	if withInfo {
		secs += 2
	}
	from, to := ss, ss+secs
	if err := tv.player.Play(it.Path, ss, limit, osd, from, to, false); err != nil {
		tv.noSignal(ch, err.Error())
		return
	}
	if withSnow && tv.cfg.StaticMS > 0 {
		end := time.Now().Add(time.Duration(tv.cfg.StaticMS) * time.Millisecond)
		for time.Now().Before(end) {
			tv.snow.Draw(tv.canvas)
			tv.present()
			time.Sleep(33 * time.Millisecond)
		}
	} else {
		tv.canvas.Fill(0, 0, tv.canvas.W, tv.canvas.H, colBlack)
		tv.present()
	}
}

// onSegmentEnded moves on when a show part or commercial finishes.
func (tv *TV) onSegmentEnded(ev exitEvent) {
	ch := tv.chans[tv.cur]
	seg := ch.Segs[tv.curSeg]
	ran := time.Since(ev.started)
	n := len(ch.Segs)
	if ran < 2500*time.Millisecond && (seg.Open || tv.segLimit > 4) {
		tv.fails++
		log.Printf("tvplay ended after %v (%v); failure %d", ran.Round(time.Millisecond), ev.err, tv.fails)
		if seg.Ad && tv.fails < 6 {
			tv.start(ch, (tv.curSeg+1)%n, 0, false, false, false) // skip a bad commercial
			return
		}
		if tv.fails >= 3 {
			why := "This video won't play. Convert it with vhsify."
			if b, err := os.ReadFile(tv.player.LogFile()); err == nil && len(b) > 0 {
				lines := strings.Split(strings.TrimSpace(string(b)), "\n")
				why = lines[len(lines)-1]
			}
			tv.noSignal(ch, why)
			return
		}
		tv.start(ch, tv.curSeg, 0, false, false, false) // retry from the top of the part
		return
	}
	tv.fails = 0
	next := (tv.curSeg + 1) % n
	si, off := ch.At(time.Now())
	switch {
	case si == tv.curSeg && (seg.Open || seg.Len-off < 20), si == next && off < 20:
		// on time (or a little behind): carry straight on
		tv.start(ch, next, 0, false, false, false)
	default:
		// we drifted (a video longer or shorter than its file says): rejoin
		if s := ch.Segs[si]; !s.Open && s.Len-off < 2 {
			si, off = (si+1)%n, 0
		}
		tv.start(ch, si, off, false, false, false)
	}
}

func (tv *TV) noSignal(ch *Channel, why string) {
	tv.player.Stop()
	tv.mode = modeNoSignal
	tv.why = why
	log.Printf("no signal on %d: %s", ch.Num, why)
	DrawNoSignal(tv.canvas, tv.fonts, &tv.snow, ch, why)
	tv.present()
}

func (tv *TV) showEmpty(title, a, b string) {
	tv.player.Stop()
	tv.mode = modeEmpty
	DrawBars(tv.canvas, tv.fonts, title, a, b)
	tv.present()
}

func (tv *TV) openGuide() {
	tv.player.Stop()
	tv.mode = modeGuide
	tv.guide.Sel = tv.cur
	tv.drawGuide()
}

func (tv *TV) drawGuide() {
	DrawGuide(tv.canvas, tv.fonts, tv.chans, &tv.guide, time.Now(), tv.cur)
	tv.present()
}

// ---------- setup menu ----------

func (tv *TV) openMenu() {
	tv.player.Stop()
	tv.mode = modeMenu
	tv.menu.open(tv.cfg, tv.adsFound())
	tv.drawMenu()
}

func (tv *TV) drawMenu() {
	DrawMenu(tv.canvas, tv.fonts, &tv.menu)
	tv.present()
}

func (tv *TV) closeMenu() {
	old := tv.cfg
	tv.cfg = tv.menu.apply(tv.cfg)
	tv.player.Fill = tv.cfg.Picture == PictureFill
	if tv.cfg != old {
		saveConfig(tv.configPath(), tv.cfg)
		if tv.cfg.Order != old.Order {
			tv.rescan()
		} else if tv.cfg.adOptions() != old.adOptions() {
			for _, c := range tv.chans {
				c.Build(tv.cfg.adOptions())
			}
		}
	}
	tv.home(true)
}

// ---------- DVR ----------

func (tv *TV) openDVR() {
	tv.player.Stop()
	tv.mode = modeDVR
	tv.drawDVR()
}

func (tv *TV) drawDVR() {
	DrawDVR(tv.canvas, tv.fonts, tv.dvr, time.Now())
	tv.present()
}

func (tv *TV) playDVR(n *Node, fromStart bool) {
	if n == nil || n.Item == nil {
		tv.drawDVR()
		return
	}
	it := n.Item
	ss := 0.0
	if !fromStart {
		ss = tv.dvr.resumeAt(it)
	}
	tv.player.Stop()
	w, h := it.W, it.H
	if w <= 0 || h <= 0 {
		w, h = 320, 240
	}
	r := videoRect(tv.fb.W, tv.fb.H, w, h, tv.player.Fill)
	osd := filepath.Join(tv.player.Work, fmt.Sprintf("osd%d.rgba", time.Now().UnixNano()%2))
	if err := writeOverlay(osd, onPanel(tv.fb.W, tv.fb.H, r, RenderPlayOSD(tv.fonts, r.Dx(), r.Dy(), it.Label(), ss))); err != nil {
		osd = ""
	}
	if err := tv.player.Play(it.Path, ss, 0, osd, ss, ss+tv.cfg.OSDSeconds+1, true); err != nil {
		tv.dvr.message = err.Error()
		tv.openDVR()
		return
	}
	tv.mode = modeDVRPlay
	tv.dvr.startPlaying(n, ss)
	tv.canvas.Fill(0, 0, tv.canvas.W, tv.canvas.H, colBlack)
	tv.present()
}

func (tv *TV) onDVREnded(ev exitEvent, menu bool) {
	if menu {
		tv.dvr.stopPlaying(false, tv.player.Position())
		tv.openDVR()
		return
	}
	n := tv.dvr.playing
	if ev.err != nil || time.Since(ev.started) < 2500*time.Millisecond {
		tv.dvr.stopPlaying(false, -1)
		tv.dvr.message = "That video wouldn't play. Convert it with vhsify."
		if b, err := os.ReadFile(tv.player.LogFile()); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			tv.dvr.message = lines[len(lines)-1]
		}
		tv.openDVR()
		return
	}
	tv.dvr.stopPlaying(true, -1) // reached the end: watched
	next := tv.dvr.nextAfter(n)
	if next == nil {
		tv.openDVR()
		return
	}
	// a moment to see what's coming, then keep watching
	tv.dvr.focus(next)
	DrawUpNext(tv.canvas, tv.fonts, next.Item.Label())
	tv.present()
	select {
	case k := <-tv.keys:
		if k&^keyRepeat == BtnB || k&^keyRepeat == BtnSelect {
			tv.openDVR()
			return
		}
		if k == BtnPower {
			tv.openDVR()
			tv.openPower()
			return
		}
	case <-time.After(2500 * time.Millisecond):
	}
	tv.playDVR(next, false)
}

// ---------- power ----------

func (tv *TV) powerOn() {
	under := NewCanvas(tv.canvas.W, tv.canvas.H)
	tv.snow.Draw(under)
	start := time.Now()
	for {
		t := time.Since(start).Seconds() / 0.45
		if t > 1 {
			break
		}
		PowerOnFrame(tv.canvas, under, t)
		tv.present()
		time.Sleep(16 * time.Millisecond)
	}
}

func (tv *TV) powerOff() {
	last := NewCanvas(tv.canvas.W, tv.canvas.H)
	if tv.mode == modeWatch || tv.mode == modeDVRPlay {
		tv.fb.CaptureFit(last)
	} else {
		copy(last.Pix, tv.canvas.Pix)
	}
	tv.player.Stop()
	if tv.mode == modeDVRPlay {
		tv.dvr.stopPlaying(false, tv.player.Position())
	}
	tv.saveState()
	start := time.Now()
	for {
		t := time.Since(start).Seconds() / 0.55
		if t > 1 {
			break
		}
		PowerOffFrame(tv.canvas, last, t)
		tv.present()
		time.Sleep(16 * time.Millisecond)
	}
	tv.canvas.Fill(0, 0, tv.canvas.W, tv.canvas.H, colBlack)
	tv.present()
	time.Sleep(150 * time.Millisecond)
}

// ---------- the power button ----------

// cancelPowerdown stops the shutdown FunKey-OS scheduled when the power
// button was pressed (what `powerdown handle` does, without the fork).
func cancelPowerdown() {
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil {
			continue
		}
		cmd := strings.ReplaceAll(string(b), "\x00", " ")
		if strings.Contains(cmd, "powerdown schedule") {
			syscall.Kill(pid, syscall.SIGTERM)
			log.Printf("power button: cancelled %q", strings.TrimSpace(cmd))
		}
	}
}

func (tv *TV) openPower() {
	under := NewCanvas(tv.canvas.W, tv.canvas.H)
	if tv.mode == modeWatch || tv.mode == modeDVRPlay {
		tv.fb.Capture(under)
	} else {
		copy(under.Pix, tv.canvas.Pix)
	}
	tv.player.Stop()
	tv.powerNode = nil
	if tv.mode == modeDVRPlay {
		tv.powerNode = tv.dvr.playing
		tv.dvr.stopPlaying(false, tv.player.Position())
	}
	tv.powerFrom = tv.mode
	tv.powerUnder = under
	tv.powerSel = powerResume
	tv.mode = modePower
	DrawPowerMenu(tv.canvas, tv.fonts, under, tv.powerSel)
	tv.present()
}

// closePower acts on the choice; true means switch off.
func (tv *TV) closePower(choice int) bool {
	switch choice {
	case powerTVOff:
		tv.mode = modeMenu // powerOff animates what's on the canvas
		copy(tv.canvas.Pix, tv.powerUnder.Pix)
		return true
	case powerShutdown:
		tv.mode = modeMenu
		copy(tv.canvas.Pix, tv.powerUnder.Pix)
		tv.powerOff()
		tv.saveState()
		log.Printf("shutting down")
		exec.Command("/usr/local/sbin/powerdown", "now").Start()
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
	// resume
	switch tv.powerFrom {
	case modeDVRPlay:
		if tv.powerNode != nil {
			tv.playDVR(tv.powerNode, false)
			return false
		}
		tv.openDVR()
	case modeDVR:
		tv.openDVR()
	case modeGuide:
		tv.mode = modeGuide
		tv.drawGuide()
	case modeMenu:
		tv.mode = modeMenu
		tv.drawMenu()
	case modeEmpty, modeNoSignal:
		tv.mode = tv.powerFrom
		copy(tv.canvas.Pix, tv.powerUnder.Pix)
		tv.present()
	default:
		tv.tune(tv.cur, false, false)
	}
	return false
}
