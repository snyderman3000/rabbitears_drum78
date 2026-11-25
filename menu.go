package main

// The SETUP menu (START): live TV or DVR, commercials, show order. Styled
// like the blue on-screen menu of a 90s TV.

import (
	"fmt"
)

var breakChoices = []float64{6, 8, 10, 12, 15, 0} // 0 = between shows only
var orderChoices = []string{OrderRotate, OrderSequential, OrderShuffle}

const (
	rowMode = iota
	rowPicture
	rowAds
	rowEvery
	rowPer
	rowOrder
	rowOff
	rowDone
	menuRows
)

type Menu struct {
	Sel   int
	mode  int // 0 live, 1 dvr
	ads   bool
	every int // index in breakChoices
	per   int
	order int // index in orderChoices
	fill  bool
	found int  // commercials on the card
	quit  bool // TURN TV OFF was chosen
}

func (m *Menu) open(cfg Config, found int) {
	m.Sel = rowMode
	m.mode = 0
	if cfg.Mode == ModeDVR {
		m.mode = 1
	}
	m.ads = cfg.Commercials
	m.every = 1
	best := 1e9
	for i, v := range breakChoices {
		d := v - cfg.AdsEveryMin
		if d < 0 {
			d = -d
		}
		if d < best {
			best, m.every = d, i
		}
	}
	m.per = cfg.AdsPerBreak
	m.order = 0
	for i, o := range orderChoices {
		if o == cfg.Order {
			m.order = i
		}
	}
	m.fill = cfg.Picture == PictureFill
	m.found = found
	m.quit = false
}

func (m *Menu) apply(cfg Config) Config {
	cfg.Mode = ModeLive
	if m.mode == 1 {
		cfg.Mode = ModeDVR
	}
	cfg.Commercials = m.ads
	cfg.AdsEveryMin = breakChoices[m.every]
	cfg.AdsPerBreak = m.per
	cfg.Order = orderChoices[m.order]
	cfg.Picture = PictureFit
	if m.fill {
		cfg.Picture = PictureFill
	}
	return cfg
}

func (m *Menu) enabled(row int) bool {
	if row == rowEvery || row == rowPer {
		return m.ads && m.mode == 0
	}
	if row == rowAds {
		return m.mode == 0
	}
	return true
}

func (m *Menu) move(d int) {
	for k := 0; k < menuRows; k++ {
		m.Sel = (m.Sel + d + menuRows) % menuRows
		if m.enabled(m.Sel) {
			return
		}
	}
}

func (m *Menu) change(d int) {
	switch m.Sel {
	case rowMode:
		m.mode = (m.mode + d + 2) % 2
	case rowPicture:
		m.fill = !m.fill
	case rowAds:
		m.ads = !m.ads
	case rowEvery:
		m.every = (m.every + d + len(breakChoices)) % len(breakChoices)
	case rowPer:
		m.per = min(6, max(1, m.per+d))
	case rowOrder:
		m.order = (m.order + d + len(orderChoices)) % len(orderChoices)
	}
}

// key handles a button; true means close the menu.
func (m *Menu) key(k int) bool {
	switch k {
	case BtnUp:
		m.move(-1)
	case BtnDown:
		m.move(1)
	case BtnLeft, BtnL1:
		m.change(-1)
	case BtnRight, BtnR1:
		m.change(1)
	case BtnA:
		if m.Sel == rowDone {
			return true
		}
		if m.Sel == rowOff {
			m.quit = true
			return true
		}
		m.change(1)
	case BtnB, BtnStart:
		return true
	}
	return false
}

func (m *Menu) label(row int) (string, string) {
	switch row {
	case rowMode:
		return "MODE", []string{"LIVE TV", "DVR"}[m.mode]
	case rowPicture:
		if m.fill {
			return "PICTURE", "ZOOM"
		}
		return "PICTURE", "4:3"
	case rowAds:
		if m.ads {
			return "ADS", "ON"
		}
		return "ADS", "OFF"
	case rowEvery:
		if breakChoices[m.every] == 0 {
			return "BREAKS", "SHOW ENDS"
		}
		return "BREAKS", fmt.Sprintf("%.0f MIN", breakChoices[m.every])
	case rowPer:
		return "PER BREAK", fmt.Sprint(m.per)
	case rowOrder:
		return "ORDER", map[string]string{OrderRotate: "ROTATE", OrderSequential: "IN ORDER", OrderShuffle: "SHUFFLE"}[orderChoices[m.order]]
	case rowOff:
		return "TURN TV OFF", ""
	}
	return "DONE", ""
}

func (m *Menu) help() string {
	switch m.Sel {
	case rowMode:
		if m.mode == 1 {
			return "DVR: pick any show and episode and watch it from the start. Picks up where you left off. No commercials."
		}
		return "Live TV: every channel is always on the air. Tune in and you land mid-show."
	case rowPicture:
		if m.fill {
			return "Zoom: the picture fills the square screen; the sides of a 4:3 show are cut off."
		}
		return "4:3: the whole picture, with black bars above and below, like a TV in a box."
	case rowAds:
		if m.found == 0 {
			return "No commercials found yet. Put them in Media/TV/Commercials (a folder named after a channel is just for that channel)."
		}
		return fmt.Sprintf("%d commercials found in Media/TV/Commercials. They play before each show and in breaks.", m.found)
	case rowOff:
		return "Switch the TV off and go back to the menu. (The power button does this too.)"
	case rowEvery:
		return "How often a show stops for a break. Breaks never land in the first or last few minutes."
	case rowPer:
		return "How many commercials play in each break."
	case rowOrder:
		switch orderChoices[m.order] {
		case OrderRotate:
			return "A channel with show folders airs one episode of each show in turn, in episode order."
		case OrderShuffle:
			return "Episodes air in a random order (an order.txt in a channel folder overrides this)."
		}
		return "Every episode in the channel folder airs in order, folder by folder."
	}
	return "Save and go back to the TV."
}
