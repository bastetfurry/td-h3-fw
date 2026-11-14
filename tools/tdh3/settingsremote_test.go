package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// simMenu answers like td-h3-fw's menu over REMOTE (fw/main.c draw_menu, menu_change, name_screen):
// every key redraws the screen; MENU, two digits, MENU, UP/DOWN, MENU, V/M.
type simMenu struct {
	out                      bytes.Buffer
	held, menu, edit, naming bool
	item, lsel               int // item: the menu number
	entry                    string
	vals                     map[int]int // menu number: index into choices
	choices                  map[int][]string
	call                     string
	name                     []byte
	drop, double             int // drop (or double) the nth UP/DOWN from now; 0: none
	keys                     int
}

func newSimMenu(t *testing.T) *simMenu {
	r := &simMenu{item: 1, vals: map[int]int{}, choices: map[int][]string{}}
	for _, s := range settingsTable {
		if s.kind == kCall {
			continue
		}
		v := "OFF"
		switch s.kind {
		case kMode:
			v = "FM"
		case kOffset:
			v = "0.60"
		case kList, kLevel:
			v = "1"
			if s.labels != nil {
				v = s.labels[1]
			}
		case kTxb:
			v = "HAM"
		case kGroup:
			v = "SCAN LIST"
		case kCal:
			v = "0"
		}
		pl, err := s.planMenu(v)
		must(t, err)
		r.choices[s.num], r.vals[s.num] = pl.choices, pl.target
	}
	r.choices[25] = []string{"OFF", "CH001", "CH005", "CH150"} // PRIORITY: only stored channels
	r.vals[25] = 0
	return r
}

func (r *simMenu) value(num int) string { return r.choices[num][r.vals[num]] }

func (r *simMenu) text(x, y, scale int, s string, fg, bg uint16) {
	r.out.Write([]byte{'t', byte(x), byte(y), byte(scale), byte(fg >> 8), byte(fg), byte(bg >> 8), byte(bg)})
	r.out.WriteString(s + "\x00")
}

func (r *simMenu) draw() {
	r.out.Write([]byte{'f', 0, 0, 128, 128, 0, 0})
	switch {
	case r.naming:
		r.text(0, 0, 1, "CALL ID", 0xFFFF, 0)
		r.text(16, 50, 2, string(r.name), 0xFFFF, 0)
		r.out.Write([]byte{'f', 16, 68, 96, 2, 0, 0})
		r.out.Write([]byte{'f', byte(16 + 12*r.lsel), 68, 10, 2, 0x05, 0x9F})
		r.text(0, 96, 1, "0-9 or \x7f\x80 letter", 0x8410, 0)
		r.text(0, 108, 1, "* < > #  MENU save", 0x8410, 0)
		r.text(0, 120, 1, "V/M back", 0x8410, 0)
	case r.menu:
		n := fmt.Sprintf("%02d", r.item)
		if r.entry != "" {
			n = r.entry + "-"
		}
		r.text(0, 0, 1, "SETUP          "+n+"/52", 0x05, 0)
		var s setting
		for _, x := range settingsTable {
			if x.num == r.item {
				s = x
			}
		}
		v := ">"
		if s.name != "" && s.kind == kCall {
			v = offOr(strings.TrimRight(r.call, " "))
		} else if s.name != "" {
			v = r.value(s.num)
			if s.kind == kMode {
				v = "VFO " + v
			}
		}
		row := s.name + strings.Repeat(" ", max(1, 21-len(s.name)-len(v))) + v
		r.text(0, 15, 1, row, 0, 0xFFFF) // the cursor row, lit
		r.text(0, 30, 1, "NEXT ITEM          1", 0xFFFF, 0)
		r.text(0, 45, 1, "ANOTHER            2", 0xFFFF, 0)
		hint := "\x7f\x80 MENU pick V/M back"
		if r.edit {
			hint = "\x7f\x80 change MENU done  "
		}
		r.text(0, 120, 1, hint, 0x8410, 0)
	default:
		r.out.Write([]byte{'t', 1, 14, 1, 0, 0, 0xFF, 0xFF, 'A', 0})
		r.text(32, 23, 2, "146.520", 0xFFFF, 0)
		r.text(0, 27, 1, "VFO", 0xFFFF, 0)
		r.text(32, 82, 2, "446.000", 0xFFFF, 0)
	}
	r.out.WriteByte('E')
}

func (r *simMenu) step(d int) {
	r.keys++
	if r.keys == r.drop {
		return
	}
	times := 1
	if r.keys == r.double {
		times = 2
	}
	for range times {
		if r.naming {
			i := max(strings.IndexByte(abc, r.name[r.lsel]), 0)
			r.name[r.lsel] = abc[(i+d+len(abc))%len(abc)]
			continue
		}
		c, v := r.choices[r.item], r.vals[r.item]+d
		if r.item >= 49 { // the CALs stop at their ends
			r.vals[r.item] = min(max(v, 0), len(c)-1)
		} else {
			r.vals[r.item] = (v + len(c)) % len(c)
		}
	}
}

func (r *simMenu) key(code byte) {
	switch {
	case r.naming:
		switch code {
		case 12, 13:
			r.step(25 - 2*int(code))
		case 16:
			r.lsel = min(r.lsel+1, 7)
		case 15:
			r.lsel = max(r.lsel-1, 0)
		case 11:
			r.call, r.naming = strings.TrimRight(string(r.name), " "), false
		case 14:
			r.naming = false
		}
	case r.menu && r.edit:
		switch code {
		case 12, 13:
			r.step(25 - 2*int(code))
		case 11, 14:
			r.edit = false
		}
	case r.menu:
		switch {
		case code <= 10:
			r.entry += strconv.Itoa(int(code - 1))
			if len(r.entry) == 2 {
				r.item, _ = strconv.Atoi(r.entry)
				r.entry = ""
			}
		case code == 11 && r.item == 20:
			r.naming, r.lsel, r.name = true, 0, []byte((r.call + "        ")[:8])
		case code == 11:
			r.edit = true
		case code == 14:
			r.menu = false
		}
	case code == 11:
		r.menu = true
	}
	r.draw()
}

func (r *simMenu) Write(b []byte) (int, error) {
	for _, c := range b {
		switch {
		case c == 'S':
			r.out.Write(sync)
			r.draw()
		case c == 'Q':
			r.out.WriteString("q146.520 -112 C - 7400 0.67.1\n")
		case c == 0x80:
			r.held = false
		case c > 0x80 && !r.held:
			r.held = true
			r.key(c & 0x7F)
		}
	}
	return len(b), nil
}
func (r *simMenu) Read(b []byte) (int, error)         { return r.out.Read(b) }
func (r *simMenu) SetReadTimeout(time.Duration) error { return nil }
func (r *simMenu) Close() error                       { return nil }

func fastKeys(t *testing.T) {
	h, g := keyHold, keyGap
	keyHold, keyGap = time.Millisecond, time.Millisecond
	t.Cleanup(func() { keyHold, keyGap = h, g })
}

func simRig(sim *simMenu) *rig { return &rig{p: sim, wait: 300 * time.Millisecond} }

// Every item in the table, to a value far from where it is, through the menu.
func TestSetMenuSetsEveryItem(t *testing.T) {
	fastKeys(t)
	sim := newSimMenu(t)
	for _, s := range settingsTable {
		v := "N0CALL"
		if s.kind != kCall {
			c := sim.choices[s.num]
			v = c[len(c)/2]
			if s.name == "REMOTE" {
				v = "ON"
			}
		}
		before, after, err := simRig(sim).setMenu(s, v)
		if err != nil {
			t.Fatalf("%s %q: %v", s.name, v, err)
		}
		got := sim.call
		if s.kind != kCall {
			got = sim.value(s.num)
		}
		if got != v || after != v || before == "" || sim.menu || sim.edit || sim.naming {
			t.Fatalf("%s: set %q, the radio holds %q (said %q -> %q); menu %v edit %v", s.name, v, got, before, after, sim.menu, sim.edit)
		}
	}
	if _, after, err := simRig(sim).setMenu(settingsTable[0], sim.value(1)); err != nil || after != sim.value(1) {
		t.Fatalf("already set: %v", err)
	}
	s, _ := findSetting("CALL ID")
	if _, after, err := simRig(sim).setMenu(s, "OFF"); err != nil || after != "OFF" || sim.call != "" {
		t.Fatalf("CALL ID OFF: %q %v, holds %q", after, err, sim.call)
	}
}

func TestSetMenuStopsOnALostOrDoubledKey(t *testing.T) {
	fastKeys(t)
	s, _ := findSetting("RX TONE")
	for _, bad := range []string{"drop", "double"} {
		sim := newSimMenu(t)
		if bad == "drop" {
			sim.drop = 2 // the second UP
		} else {
			sim.double = 2
		}
		_, _, err := simRig(sim).setMenu(s, "100.0")
		if err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatalf("%s: err = %v", bad, err)
		}
		if sim.menu || sim.edit {
			t.Fatalf("%s: left in the menu", bad)
		}
	}
}

func TestSetMenuRefusals(t *testing.T) {
	fastKeys(t)
	sim := newSimMenu(t)
	s, _ := findSetting("PRIORITY")
	if _, _, err := simRig(sim).setMenu(s, "CH005"); err != nil || sim.value(25) != "CH005" {
		t.Fatalf("PRIORITY 5: %v, holds %s", err, sim.value(25))
	}
	if _, _, err := simRig(sim).setMenu(s, "100"); err == nil || !strings.Contains(err.Error(), "empty channel") {
		t.Fatalf("PRIORITY on an empty channel: %v", err)
	}
	sim = newSimMenu(t)
	for _, c := range [][2]string{{"REMOTE", "OFF"}, {"SCAN GROUP", "1,3"}, {"MODE", "LSB"}, {"OFFSET", "0.62"}, {"POWER", "huge"}} {
		s, err := findSetting(c[0])
		if err != nil {
			s, _ = findSetting("OFFSET MHz")
		}
		if _, _, err := simRig(sim).setMenu(s, c[1]); err == nil {
			t.Errorf("%s %s taken", c[0], c[1])
		}
	}
	if sim.out.Len() != 0 || sim.keys != 0 {
		t.Fatal("refused, but the radio was talked to")
	}
}

// runSet goes over REMOTE when the radio answers it, and never opens PC LINK then.
func TestSettingsSetOverRemote(t *testing.T) {
	fastKeys(t)
	sim := newSimMenu(t)
	old, oldC := findRemote, openClone
	findRemote = func(string) (clonePort, bool) { return sim, true }
	openClone = func(string) (clonePort, error) { t.Fatal("PC LINK opened"); return nil, nil }
	t.Cleanup(func() { findRemote, openClone = old, oldC })
	must(t, runSet("fake", "brightness", "10"))
	if sim.value(37) != "10" {
		t.Fatalf("BRIGHTNESS holds %s", sim.value(37))
	}
	must(t, runSet("fake", "MODE", "am")) // kept per slot: only the menu can set it
	if sim.value(6) != "AM" {
		t.Fatalf("MODE holds %s", sim.value(6))
	}
}
