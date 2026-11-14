// tdh3 settings set over REMOTE: the radio's own menu, pressed and read back a key at a time, as a
// person would (MENU, the item's two digits, MENU, UP/DOWN to the value, MENU, V/M). For a radio
// that isn't in PC LINK, or whose screen can't be read by eye.
package main

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// findRemote is the radio when it answers REMOTE on the port. A var: tests stand in for the radio.
var findRemote = func(name string) (clonePort, bool) {
	p, ok, _ := probeRemote(name, max(400*time.Millisecond, remoteWait/2)) // a busy radio answers late
	if !ok {
		return nil, false
	}
	return p, true
}

// menuPlan is how the menu gets to a value: the values UP steps through (DOWN back), in order.
type menuPlan struct {
	choices []string
	target  int
	clamp   bool   // the CALs stop at their ends; the rest wrap round
	skips   bool   // PRIORITY steps over empty channels
	call    string // CALL ID: its 8 characters, typed on its own screen
}

// abc is fw/main.c's name alphabet: UP/DOWN on the CALL ID screen step through it.
const abc = " ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-/."

// planMenu checks VALUE and works out the steps, before a key is pressed.
func (s setting) planMenu(v string) (menuPlan, error) {
	blank := func() []byte { m := make([]byte, 0x2000); return m }
	n := norm(v)
	switch s.kind {
	case kMode:
		if i := slices.Index(modeS, n); i >= 0 {
			return menuPlan{choices: modeS, target: i}, nil
		}
		return menuPlan{}, fmt.Errorf("MODE %q: give FM, AM or USB (the selected slot's)", v)
	case kOffset:
		var pl menuPlan
		for x := 1; x <= 255; x++ {
			pl.choices = append(pl.choices, mhz50(x))
		}
		f, err := strconv.ParseFloat(strings.TrimSuffix(n, "MHZ"), 64)
		x := int(math.Round(f / 0.05))
		if err != nil || x < 1 || x > 255 || math.Abs(float64(x)*0.05-f) > 1e-6 {
			return menuPlan{}, fmt.Errorf("OFFSET MHz %q: give 0.05 to 12.75 in steps of 0.05 (the selected slot's band)", v)
		}
		pl.target = x - 1
		return pl, nil
	case kCall:
		es, err := s.parseValue(v)
		if err != nil {
			return menuPlan{}, err
		}
		b := make([]byte, 8)
		for i, e := range es {
			b[i] = max(e.val, ' ')
		}
		return menuPlan{call: string(b)}, nil
	}
	es, err := s.parseValue(v)
	if err != nil {
		return menuPlan{}, err
	}
	if s.name == "REMOTE" && n == "OFF" {
		return menuPlan{}, errors.New("turning REMOTE off over REMOTE would cut the link half way: on the radio press MENU, 4 7, MENU, DOWN, MENU")
	}
	if s.kind == kGroup && es[0].val&(es[0].val-1) != 0 {
		return menuPlan{}, errors.New("over REMOTE, SCAN GROUP takes one group at a time: for several, use GROUP LINK on the radio (MENU, 2 6) or PC LINK")
	}
	var xs []int // the raw values in the order UP steps through them
	pl := menuPlan{clamp: s.kind == kCal, skips: s.kind == kPrio}
	switch s.kind {
	case kTone:
		xs = make([]int, nToneMenu)
	case kGroup:
		xs = []int{0, 1, 2, 4, 8, 16, 32, 64, 128}
	case kPrio:
		xs = make([]int, maxChan+1)
	case kCal:
		for x := s.lo; x <= s.hi; x++ {
			xs = append(xs, x)
		}
	default:
		xs = make([]int, s.n)
	}
	for i := range xs {
		if s.kind != kGroup && s.kind != kCal {
			xs[i] = i
		}
	}
	at := cfgAddr + s.off
	for _, x := range xs {
		m := blank()
		m[at] = byte(x << s.shift)
		if s.kind == kTone {
			m[at+1] = byte(x >> 8)
		}
		pl.choices = append(pl.choices, s.value(m))
	}
	pl.target = slices.Index(pl.choices, s.value(withEdits(blank(), es)))
	if pl.target < 0 {
		return menuPlan{}, fmt.Errorf("%s %q: the menu has no such value", s.name, v)
	}
	return pl, nil
}

// menuItem is the item number in the menu's title ("RECEIVE ----- 09/52"), 0 when there is none.
func menuItem(sc *screen) int {
	t, _ := sc.at(0, 0)
	f := strings.Fields(t)
	var m, n int
	if len(f) == 0 {
		return 0
	}
	if _, err := fmt.Sscanf(f[len(f)-1], "%d/%d", &m, &n); err != nil || m < 1 || m > n {
		return 0
	}
	return m
}

// litRow is the menu's cursor row: the item's name, then its value right-aligned.
func litRow(sc *screen) string {
	for k := range 6 {
		if s, lit := sc.at(15+15*k, 0); lit && s != "" {
			return s
		}
	}
	return ""
}

// rowValue is the cursor row's value when the row is item name ("VFO " in front left out).
func rowValue(sc *screen, name string) (string, bool) {
	r := litRow(sc)
	if !strings.HasPrefix(r, name+" ") {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimSpace(r[len(name):]), "VFO "), true
}

func editing(sc *screen) bool { return strings.Contains(sc.bottom(), "change") }

// setMenu sets s to v on the radio's menu and goes home. It returns the value before and after.
func (r *rig) setMenu(s setting, v string) (before, after string, err error) {
	pl, err := s.planMenu(v)
	if err != nil {
		return "", "", err
	}
	if _, err := r.home(); err != nil {
		return "", "", err
	}
	defer func() { // home whatever happened: V/M leaves a change as it stands, or a CALL ID unsaved
		if _, herr := r.home(); err == nil {
			err = herr
		}
	}()
	open := func(sc *screen) bool { return menuItem(sc) > 0 && litRow(sc) != "" } // as settings() checks
	sc, err := r.key("MENU", open)
	if err == nil {
		sc, err = r.sure(sc, open)
	}
	if err != nil {
		return "", "", err
	}
	if !open(sc) {
		return "", "", errors.New("MENU did not open the menu")
	}
	d := fmt.Sprintf("%02d", s.num)
	on := func(sc *screen) bool { _, ok := rowValue(sc, s.name); return menuItem(sc) == s.num && ok }
	// Both digits at once: one alone jumps after a second, and reading the screen between them can
	// take that long. The title and the cursor row say where it landed.
	if err = press(r.p, []string{d[:1], d[1:]}); err == nil {
		if sc, err = r.settle(on); err == nil {
			sc, err = r.sure(sc, on)
		}
	}
	if err != nil {
		return "", "", err
	}
	if !on(sc) {
		return "", "", fmt.Errorf("typed %s in the menu, the cursor is on item %d (%q), not %s", d, menuItem(sc), litRow(sc), s.name)
	}
	before, _ = rowValue(sc, s.name)
	if s.kind == kCall {
		after, err = r.typeCall(s, pl.call)
		return before, after, err
	}
	if slices.Index(pl.choices, before) == pl.target {
		return before, before, nil
	}
	edit := func(sc *screen) bool { return on(sc) && editing(sc) }
	if sc, err = r.key("MENU", edit); err == nil {
		sc, err = r.sure(sc, edit)
	}
	if err == nil && !edit(sc) {
		err = fmt.Errorf("MENU on %s did not light its value for changing", s.name)
	}
	if err != nil {
		return before, "", err
	}
	cur := before
	for presses := 0; ; presses++ {
		i := slices.Index(pl.choices, cur)
		if i < 0 {
			return before, cur, fmt.Errorf("the radio shows %q for %s, which tdh3 doesn't know: stopped", cur, s.name)
		}
		if i == pl.target {
			break
		}
		if presses > len(pl.choices)+2 {
			return before, cur, fmt.Errorf("stopped at %s: %s never got to %s", cur, s.name, pl.choices[pl.target])
		}
		n := len(pl.choices)
		up, down := (pl.target-i+n)%n, (i-pl.target+n)%n
		k, dir, dist := "UP", 1, up
		if pl.clamp && pl.target < i || !pl.clamp && down < up {
			k, dir, dist = "DOWN", -1, down
		}
		want := pl.choices[(i+dir+n)%n]
		moved := func(sc *screen) bool { v, ok := rowValue(sc, s.name); return ok && v != cur }
		shows := func(sc *screen) bool {
			v, ok := rowValue(sc, s.name)
			return ok && (v == want || pl.skips && v != cur)
		}
		if sc, err = r.key(k, moved); err == nil {
			sc, err = r.sure(sc, shows)
		}
		if err != nil {
			return before, cur, err
		}
		v, _ := rowValue(sc, s.name)
		j := slices.Index(pl.choices, v)
		if !shows(sc) || j < 0 {
			return before, cur, fmt.Errorf("pressed %s on %s at %s: the screen shows %q, not %s: stopped (a key got lost or doubled)", k, s.name, cur, v, want)
		}
		if pl.skips && ((j-i)*dir+n)%n > dist {
			return before, v, fmt.Errorf("the radio went from %s to %s for %s: %s is an empty channel", cur, v, s.name, pl.choices[pl.target])
		}
		cur = v
	}
	done := func(sc *screen) bool { v, ok := rowValue(sc, s.name); return ok && !editing(sc) && v == cur }
	if sc, err = r.key("MENU", done); err == nil {
		sc, err = r.sure(sc, done)
	}
	if err == nil && !done(sc) {
		err = fmt.Errorf("MENU did not finish the change: the row shows %q", litRow(sc))
	}
	return before, cur, err
}

// typeCall types the CALL ID on its screen (fw/main.c name_screen): UP/DOWN change the letter over
// the cursor, # moves it right, MENU saves. The name is 2x at (16, 50), the cursor a bar at y 68.
func (r *rig) typeCall(s setting, call string) (string, error) {
	name := func(sc *screen) string { t, _ := sc.at(50, 16); return (t + "        ")[:8] }
	cursor := func(sc *screen) int {
		bg := sc.px[68][27] // between the first and second marks: never the cursor
		for i := range 8 {
			if sc.px[68][16+12*i] != bg {
				return i
			}
		}
		return -1
	}
	at := func(pos int) func(*screen) bool {
		return func(sc *screen) bool {
			t, _ := sc.at(0, 0)
			return strings.HasPrefix(t, s.name) && sc.bottom() == "V/M back" && cursor(sc) == pos
		}
	}
	sc, err := r.key("MENU", at(0))
	if err == nil {
		sc, err = r.sure(sc, at(0))
	}
	if err == nil && !at(0)(sc) {
		err = errors.New("MENU on CALL ID did not open its screen")
	}
	if err != nil {
		return "", err
	}
	for pos := range 8 {
		for presses := 0; name(sc)[pos] != call[pos]; presses++ {
			c := name(sc)[pos]
			i, t, n := max(strings.IndexByte(abc, c), 0), strings.IndexByte(abc, call[pos]), len(abc)
			k, dir := "UP", 1
			if (i-t+n)%n < (t-i+n)%n {
				k, dir = "DOWN", -1
			}
			want := abc[(i+dir+n)%n]
			shows := func(sc *screen) bool { return name(sc)[pos] == want && cursor(sc) == pos }
			if presses > n {
				return "", fmt.Errorf("CALL ID: letter %d never got to %q", pos+1, call[pos])
			}
			if sc, err = r.key(k, shows); err == nil {
				sc, err = r.sure(sc, shows)
			}
			if err != nil {
				return "", err
			}
			if !shows(sc) {
				return "", fmt.Errorf("pressed %s on CALL ID letter %d: the screen shows %q, not %q: stopped, nothing saved", k, pos+1, name(sc), want)
			}
		}
		if pos == 7 || strings.TrimRight(call[pos+1:], " ") == "" && strings.TrimRight(name(sc)[pos+1:], " ") == "" {
			break
		}
		if sc, err = r.key("#", at(pos+1)); err == nil {
			sc, err = r.sure(sc, at(pos+1))
		}
		if err != nil {
			return "", err
		}
		if !at(pos + 1)(sc) {
			return "", fmt.Errorf("# on CALL ID did not move to letter %d: stopped, nothing saved", pos+2)
		}
	}
	want := strings.TrimRight(call, " ")
	if want == "" {
		want = "OFF"
	}
	saved := func(sc *screen) bool { v, ok := rowValue(sc, s.name); return ok && menuItem(sc) == s.num && v == want }
	if sc, err = r.key("MENU", saved); err == nil {
		sc, err = r.sure(sc, saved)
	}
	if err == nil && !saved(sc) {
		err = fmt.Errorf("MENU did not save the CALL ID: the menu shows %q", litRow(sc))
	}
	return want, err
}

// remoteWait is how long a screen read waits (settings set --wait).
var remoteWait = 1500 * time.Millisecond

// remoteSet is runSet for a radio with REMOTE on.
func remoteSet(p port, s setting, v string) error {
	okLine("REMOTE answered: setting " + s.name + " on the radio's menu")
	before, after, err := (&rig{p: p, wait: remoteWait}).setMenu(s, v)
	if err != nil {
		return err
	}
	if before == after {
		okLine(s.name + " is already " + after + ": nothing was changed")
		return nil
	}
	okLine(s.name + ": " + before + " -> " + after + " (set on the radio's menu and read back)")
	return nil
}
