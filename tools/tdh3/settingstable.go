// The radio's menu settings by name: where each lives in the settings block (cfg, 0x1FC0-0x1FEF in
// fw/main.c) and how the menu shows it (menu_value). The table is written out here, and
// TestSettingsTableMatchesTheFirmware reads fw/main.c and fails when the two disagree: a menu
// number, a name, a byte, its bits, the number of values, the default or a label.
package main

import (
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type setKind int

const (
	kList   setKind = iota // one of labels (or a digit when there are none)
	kLevel                 // SQUELCH: 0-9, then S1-S9
	kVox                   // VOX: OFF, then 1-9
	kTxb                   // TX BANDS: a bit per band
	kWx                    // WX ALERT: OFF or a weather channel's frequency
	kMode                  // MODE: one per slot, A bits 4:3, B 6:5
	kTone                  // RX TONE, TX TONE: an index into the tone menu, 16 bits
	kOffset                // OFFSET MHz: one per band, 50 kHz steps, 0 the band's standard
	kGroup                 // SCAN GROUP: bit g-1 for group g, 0 the scan list
	kPrio                  // PRIORITY: OFF or a channel
	kCal                   // FREQ CAL, METER CAL, BATT CAL: signed
	kCall                  // CALL ID: 8 characters at 0x1E00
)

// setting is one menu item that holds a value. off is its byte in cfg; shift and width its bits
// there (kList, kLevel, kVox, kTxb, kWx: the firmware's items[] entry); n values, def the default.
type setting struct {
	num          int // its menu number
	name         string
	kind         setKind
	field        string // the cfg field, for the firmware check
	off          int
	shift, width int
	n, def       int
	labels       []string
	lo, hi       int     // kCal: the range the firmware keeps
	unit         float64 // kCal: one step
	fmtc         string  // kCal: how the menu shows it
}

var (
	powS    = []string{"ULTRA LOW", "LOW", "MID", "HIGH"}
	bwS     = []string{"WIDE", "NARROW"}
	modeS   = []string{"FM", "AM", "USB"}
	shiftS  = []string{"OFF", "+", "-", "AUTO"}
	stepS   = []string{"2.5", "5", "6.25", "10", "12.5", "20", "25", "50", "100", "8.33", "15", "30"}
	pauseS  = []string{"2 s", "5 s", "10 s", "20 s", "BUSY"}
	resumeS = []string{"2 s", "3 s", "5 s", "HOLD", "0 s", "1 s"}
	saveS   = []string{"OFF", "SHORT", "MIDDLE", "LONG"}
	totS    = []string{"OFF", "030 s", "060 s", "120 s", "180 s"}
	lightS  = []string{"10 s", "30 s", "ON"}
	brightS = []string{"10", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	apoS    = []string{"OFF", "30 min", "60 min", "120 min"}
	offOn   = []string{"OFF", "ON"}
	compS   = []string{"OFF", "RX", "TX", "BOTH"}
	tailS   = []string{"ON", "OFF", "REVERSE"}
	narS    = []string{"12.5", "6.25"}
	klockS  = []string{"KEYS", "KEYS+PTT"}
	voxdS   = []string{"1 s", "1.5 s", "2 s", "3 s", "0.5 s"}
	themeS  = []string{"CLASSIC", "NIGHT AMBER", "GREEN", "NIGHT RED", "POCKET", "NIXIE", "E-INK", "FIELD", "HIGH-VIS"}
	digitsS = []string{"ARCADE", "LCD", "CLASSIC"}
	bandS   = []string{"HAM", "GMRS", "MURS", "FRS"} // TX BANDS bits 0-3

	// The tone menu: OFF, the CTCSS tones (0.1 Hz), the DCS codes normal then inverted (main.c tones, dcs_lo).
	ctcssTones = []int{670, 693, 719, 744, 770, 797, 825, 854, 885, 915, 948, 974, 1000, 1035, 1072, 1109, 1148, 1188,
		1230, 1273, 1318, 1365, 1413, 1462, 1514, 1567, 1598, 1622, 1655, 1679, 1713, 1738, 1773, 1799,
		1835, 1862, 1899, 1928, 1966, 1995, 2035, 2065, 2107, 2181, 2257, 2291, 2336, 2418, 2503, 2541}
	dcsCodes = []int{0o23, 0o25, 0o26, 0o31, 0o32, 0o36, 0o43, 0o47, 0o51, 0o53, 0o54, 0o65, 0o71, 0o72, 0o73, 0o74,
		0o114, 0o115, 0o116, 0o122, 0o125, 0o131, 0o132, 0o134, 0o143, 0o145, 0o152, 0o155, 0o156, 0o162, 0o165, 0o172,
		0o174, 0o205, 0o212, 0o223, 0o225, 0o226, 0o243, 0o244, 0o245, 0o246, 0o251, 0o252, 0o255, 0o261, 0o263, 0o265,
		0o266, 0o271, 0o274, 0o306, 0o311, 0o315, 0o325, 0o331, 0o332, 0o343, 0o346, 0o351, 0o356, 0o364, 0o365, 0o371,
		0o411, 0o412, 0o413, 0o423, 0o431, 0o432, 0o445, 0o446, 0o452, 0o454, 0o455, 0o462, 0o464, 0o465, 0o466, 0o503,
		0o506, 0o516, 0o523, 0o526, 0o532, 0o546, 0o565, 0o606, 0o612, 0o624, 0o627, 0o631, 0o632, 0o654, 0o662, 0o664,
		0o703, 0o712, 0o723, 0o731, 0o732, 0o734, 0o743, 0o754}
	wxFreqs = []uint32{16240000, 16242500, 16245000, 16247500, 16250000, 16252500, 16255000} // main.c wx_f, 10 Hz
)

// settingsTable is every menu item with a value, in menu order.
var settingsTable = []setting{
	{num: 1, name: "SQUELCH", kind: kLevel, field: "sql", off: 1, width: 8, n: 19, def: 3},
	{num: 2, name: "ATTENUATOR", kind: kList, off: 4, shift: 1, width: 1, n: 2, def: 0, labels: offOn},
	{num: 3, name: "COMPANDER", kind: kList, off: 9, shift: 0, width: 2, n: 4, def: 0, labels: compS},
	{num: 4, name: "NARROW kHz", kind: kList, off: 9, shift: 4, width: 1, n: 2, def: 0, labels: narS},
	{num: 5, name: "BANDWIDTH", kind: kList, off: 4, shift: 0, width: 1, n: 2, def: 0, labels: bwS},
	{num: 6, name: "MODE", kind: kMode, off: 8, shift: 3, width: 2, n: 3, def: 0, labels: modeS},
	{num: 7, name: "STEP kHz", kind: kList, off: 2, shift: 0, width: 4, n: 12, def: 6, labels: stepS},
	{num: 8, name: "RX TONE", kind: kTone, field: "rxt", off: 23},
	{num: 9, name: "POWER", kind: kList, off: 3, shift: 0, width: 2, n: 4, def: 1, labels: powS},
	{num: 10, name: "SHIFT", kind: kList, off: 3, shift: 2, width: 2, n: 4, def: 3, labels: shiftS},
	{num: 11, name: "OFFSET MHz", kind: kOffset, field: "off", off: 10},
	{num: 12, name: "TX TONE", kind: kTone, field: "txt", off: 25},
	{num: 13, name: "TX BANDS", kind: kTxb, field: "txb", off: 17, width: 8, n: 16, def: 3},
	{num: 14, name: "TX TIMEOUT", kind: kList, field: "tot", off: 7, width: 8, n: 5, def: 3, labels: totS},
	{num: 15, name: "TAIL TONE", kind: kList, off: 9, shift: 2, width: 2, n: 3, def: 0, labels: tailS},
	{num: 16, name: "MIC GAIN", kind: kList, off: 2, shift: 4, width: 4, n: 10, def: 5},
	{num: 17, name: "VOX", kind: kVox, off: 16, shift: 0, width: 4, n: 10, def: 0},
	{num: 18, name: "VOX DELAY", kind: kList, off: 16, shift: 4, width: 3, n: 5, def: 0, labels: voxdS},
	{num: 20, name: "CALL ID", kind: kCall},
	{num: 21, name: "SCAN GROUP", kind: kGroup, field: "group", off: 18},
	{num: 22, name: "SCAN PAUSE", kind: kList, off: 6, shift: 0, width: 3, n: 5, def: 4, labels: pauseS},
	{num: 23, name: "SCAN RESUME", kind: kList, off: 6, shift: 3, width: 3, n: 6, def: 2, labels: resumeS},
	{num: 24, name: "DUAL WATCH", kind: kList, off: 4, shift: 2, width: 1, n: 2, def: 1, labels: offOn},
	{num: 25, name: "PRIORITY", kind: kPrio, field: "prio", off: 14},
	{num: 37, name: "LIGHT", kind: kList, off: 3, shift: 6, width: 2, n: 3, def: 1, labels: lightS},
	{num: 38, name: "BRIGHTNESS", kind: kList, field: "bright", off: 12, shift: 4, width: 4, n: 10, def: 0, labels: brightS},
	{num: 39, name: "THEME", kind: kList, field: "theme", off: 12, shift: 0, width: 4, n: 9, def: 0, labels: themeS},
	{num: 40, name: "DIGITS", kind: kList, off: 6, shift: 6, width: 2, n: 3, def: 0, labels: digitsS},
	{num: 41, name: "KEY BEEP", kind: kList, off: 4, shift: 4, width: 1, n: 2, def: 1, labels: offOn},
	{num: 42, name: "KEY LOCK", kind: kList, off: 5, shift: 7, width: 1, n: 2, def: 0, labels: klockS},
	{num: 43, name: "BELL", kind: kList, off: 4, shift: 7, width: 1, n: 2, def: 0, labels: offOn},
	{num: 44, name: "WX ALERT", kind: kWx, field: "wx", off: 15, width: 8, n: 8, def: 0},
	{num: 45, name: "AUTO OFF", kind: kList, off: 5, shift: 0, width: 2, n: 4, def: 0, labels: apoS},
	{num: 46, name: "BATT SAVE", kind: kList, off: 3, shift: 4, width: 2, n: 4, def: 2, labels: saveS},
	{num: 48, name: "REMOTE", kind: kList, off: 4, shift: 6, width: 1, n: 2, def: 0, labels: offOn},
	{num: 49, name: "BLUETOOTH", kind: kList, off: 4, shift: 5, width: 1, n: 2, def: 0, labels: offOn},
	{num: 50, name: "FREQ CAL", kind: kCal, field: "fcal", off: 20, lo: -100, hi: 100, unit: 0.1, fmtc: "%+.1f ppm"},
	{num: 51, name: "METER CAL", kind: kCal, field: "mcal", off: 21, lo: -40, hi: 40, unit: 1, fmtc: "%+.0f dB"},
	{num: 52, name: "BATT CAL", kind: kCal, field: "bcal", off: 22, lo: -50, hi: 50, unit: 0.02, fmtc: "%+.2f V"},
}

// The settings' other bytes, not menu items: which slot is selected, each slot, the FM station.
const (
	cfgAct = 13 // 0 A, 1 B
	cfgFM  = 27 // 16 bits, 100 kHz
	cfgS   = 29 // slot_t s[2]: vfo (32 bits, 10 Hz), mem (1: on a channel), chn
)

// cfgOf is the settings block of a backup's EEPROM.
func cfgOf(mem []byte) []byte { return mem[cfgAddr:0x1FF0] }

func (s setting) raw(c []byte) int { return int(c[s.off]) >> s.shift & (1<<s.width - 1) }

// list is a kList/kLevel/kVox/kTxb/kWx value as the radio loads it: out of range is the default (cfg_load).
func (s setting) list(c []byte) int {
	if v := s.raw(c); v < s.n {
		return v
	}
	return s.def
}

func toneIndex(i int) uint16 { // main.c tone_of
	switch {
	case i <= 0:
		return 0
	case i <= len(ctcssTones):
		return uint16(ctcssTones[i-1])
	}
	i -= len(ctcssTones) + 1
	if i < len(dcsCodes) {
		return 0x8000 | uint16(dcsCodes[i])
	}
	return 0xC000 | uint16(dcsCodes[i-len(dcsCodes)])
}

const nToneMenu = 1 + 50 + 2*104

func toneText(t uint16) string { // main.c tone_s
	switch {
	case t == 0:
		return "OFF"
	case t&0x8000 != 0:
		pol := "N"
		if t&0x4000 != 0 {
			pol = "I"
		}
		return fmt.Sprintf("D%03o%s", t&0x1FF, pol)
	}
	return fmt.Sprintf("%d.%d", t/10, t%10)
}

func offsetN(c []byte, uhf int) int { // main.c off_n
	if v := int(c[10+uhf]); v != 0 {
		return v
	}
	return []int{12, 100}[uhf]
}

func mhz50(n int) string { return fmt.Sprintf("%d.%02d", n*5/100, n*5%100) }

func wxText(v int) string { // OFF, or WXv's frequency as freq_s shows it
	if v == 0 {
		return "OFF"
	}
	f := wxFreqs[v-1]
	return fmt.Sprintf("%d.%03d", f/100000, f/100%1000)
}

func levelText(v int) string { // main.c level_s
	if v > 9 {
		return "S" + strconv.Itoa(v-9)
	}
	return strconv.Itoa(v)
}

func groupText(g byte) string { // main.c groups_s, word 1
	if g == 0 {
		return "SCAN LIST"
	}
	var d []byte
	for i := range 8 {
		if g>>i&1 == 1 {
			d = append(d, '1'+byte(i))
		}
	}
	if g&(g-1) == 0 {
		return "GROUP " + string(d)
	}
	return "G" + string(d)
}

func txbText(v int) string { // main.c txb_s
	var on []string
	for b, n := range bandS {
		if v>>b&1 == 1 {
			on = append(on, n)
		}
	}
	switch {
	case len(on) == 0:
		return "NONE"
	case len(on) >= 3:
		for i := range on {
			on[i] = on[i][:1]
		}
	}
	return strings.Join(on, " ")
}

// value is the setting in mem as the menu shows it.
func (s setting) value(mem []byte) string {
	c := cfgOf(mem)
	switch s.kind {
	case kList:
		v := s.list(c)
		if s.labels == nil {
			return strconv.Itoa(v)
		}
		return s.labels[v]
	case kLevel:
		return levelText(s.list(c))
	case kVox:
		if v := s.list(c); v != 0 {
			return strconv.Itoa(v)
		}
		return "OFF"
	case kTxb:
		return txbText(s.list(c))
	case kWx:
		return wxText(s.list(c))
	case kMode:
		var out []string
		for i, slot := range []string{"A", "B"} {
			v := int(c[s.off]) >> (s.shift + 2*i) & 3
			if v >= s.n {
				v = s.def
			}
			out = append(out, slot+" "+s.labels[v])
		}
		return strings.Join(out, ", ")
	case kTone:
		i := int(binary.LittleEndian.Uint16(c[s.off:]))
		if i >= nToneMenu {
			i = 0
		}
		return toneText(toneIndex(i))
	case kOffset:
		return mhz50(offsetN(c, 0)) + " below 300 MHz, " + mhz50(offsetN(c, 1)) + " from 300 MHz"
	case kGroup:
		return groupText(c[s.off])
	case kPrio:
		if p := int(c[s.off]); p > 0 && p <= maxChan {
			return fmt.Sprintf("CH%03d", p)
		}
		return "OFF"
	case kCal:
		v := int(int8(c[s.off]))
		if v < s.lo || v > s.hi {
			v = 0
		}
		return fmt.Sprintf(s.fmtc, float64(v)*s.unit)
	case kCall:
		return offOr(callID(mem))
	}
	return "?"
}

// slotText is slot i (0 A, 1 B) as a VFO frequency or a channel.
func slotText(mem []byte, i int) string {
	c := cfgOf(mem)[cfgS+6*i:]
	if c[4] == 1 {
		return fmt.Sprintf("channel %d", c[5])
	}
	return mhz(binary.LittleEndian.Uint32(c)) + " MHz"
}

// otherSettings are the settings bytes that aren't menu items, as name and value.
func otherSettings(mem []byte) [][2]string {
	c := cfgOf(mem)
	fm := int(binary.LittleEndian.Uint16(c[cfgFM:]))
	return [][2]string{
		{"selected slot", "AB"[c[cfgAct]&1 : c[cfgAct]&1+1]},
		{"slot A", slotText(mem, 0)},
		{"slot B", slotText(mem, 1)},
		{"FM radio station", fmt.Sprintf("%d.%d MHz", fm/10, fm%10)},
	}
}

// hasSettings says whether mem holds td-h3-fw's settings.
func hasSettings(mem []byte) bool { return slices.Contains(settingsMagics, mem[cfgAddr]) }

// findSetting finds a setting by its menu name (any case, spaces optional) or menu number.
func findSetting(name string) (setting, error) {
	norm := func(s string) string { return strings.ToUpper(strings.ReplaceAll(s, " ", "")) }
	n, numErr := strconv.Atoi(name)
	for _, s := range settingsTable {
		if numErr == nil && s.num == n || norm(s.name) == norm(name) {
			return s, nil
		}
	}
	return setting{}, fmt.Errorf("no setting %q: tdh3 settings show lists them", name)
}
