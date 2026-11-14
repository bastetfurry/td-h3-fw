// tdh3 settings show and settings set: the menu settings by name (settingstable.go).
package main

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// norm makes a typed value comparable with a label: upper case, no spaces.
func norm(s string) string { return strings.ToUpper(strings.Join(strings.Fields(s), "")) }

// parseValue turns VALUE, as the menu shows it, into byte edits of the settings (or the CALL ID).
func (s setting) parseValue(v string) ([]edit, error) {
	at := cfgAddr + s.off
	bits := func(x int) []edit {
		m := byte((1<<s.width - 1) << s.shift)
		return []edit{{at, m, byte(x << s.shift)}}
	}
	bad := func(want string) error { return fmt.Errorf("%s %q: give %s", s.name, v, want) }
	n := norm(v)
	switch s.kind {
	case kList:
		if s.labels == nil {
			x, err := strconv.Atoi(n)
			if err != nil || x < 0 || x >= s.n {
				return nil, bad(fmt.Sprintf("0-%d", s.n-1))
			}
			return bits(x), nil
		}
		for i, l := range s.labels {
			if norm(l) == n || strings.TrimLeft(norm(l), "0") == strings.TrimLeft(n, "0") {
				return bits(i), nil
			}
		}
		return nil, bad(strings.Join(s.labels, ", "))
	case kLevel, kVox:
		for x := range s.n {
			t := levelText(x)
			if s.kind == kVox && x == 0 {
				t = "OFF"
			}
			if norm(t) == n {
				return bits(x), nil
			}
		}
		if s.kind == kLevel {
			return nil, bad("0-9 or S1-S9")
		}
		return nil, bad("OFF or 1-9")
	case kTxb:
		x := 0
		if n != "NONE" && n != "OFF" {
			for _, w := range strings.FieldsFunc(strings.ToUpper(v), func(r rune) bool { return r == ',' || r == ' ' || r == '+' }) {
				i := slices.IndexFunc(bandS, func(b string) bool { return b == w || b[:1] == w })
				if i < 0 {
					return nil, bad("NONE, or bands from HAM, GMRS, MURS, FRS, like HAM,GMRS")
				}
				x |= 1 << i
			}
		}
		return bits(x), nil
	case kWx:
		for x := range s.n {
			if norm(wxText(x)) == n || x > 0 && n == "WX"+strconv.Itoa(x) {
				return bits(x), nil
			}
		}
		return nil, bad("OFF, WX1-WX7 or a weather frequency like 162.550")
	case kTone:
		for i := range nToneMenu {
			t := toneText(toneIndex(i))
			if norm(t) == n || t == "OFF" && n == "NONE" || i > 0 && i <= 50 && sameNumber(t, n) {
				return []edit{{at, 0xFF, byte(i)}, {at + 1, 0xFF, byte(i >> 8)}}, nil
			}
		}
		return nil, bad("OFF, a CTCSS tone like 100.0, or a DCS code like D023N or D023I")
	case kGroup:
		if n == "SCANLIST" || n == "OFF" || n == "NONE" {
			return []edit{{at, 0xFF, 0}}, nil
		}
		x := 0
		for _, r := range strings.TrimPrefix(strings.TrimPrefix(n, "GROUP"), "G") {
			if r == ',' {
				continue
			}
			if r < '1' || r > '8' {
				return nil, bad("SCAN LIST, or groups 1-8 like 1,3")
			}
			x |= 1 << (r - '1')
		}
		if x == 0 {
			return nil, bad("SCAN LIST, or groups 1-8 like 1,3")
		}
		return []edit{{at, 0xFF, byte(x)}}, nil
	case kPrio:
		if n == "OFF" {
			return []edit{{at, 0xFF, 0}}, nil
		}
		x, err := strconv.Atoi(strings.TrimPrefix(n, "CH"))
		if err != nil || x < 1 || x > maxChan {
			return nil, bad("OFF or a channel 1-199")
		}
		return []edit{{at, 0xFF, byte(x)}}, nil
	case kCal:
		f, err := strconv.ParseFloat(strings.TrimRight(n, "PMDBV"), 64)
		x := int(math.Round(f / s.unit))
		if err != nil || x < s.lo || x > s.hi || math.Abs(float64(x)*s.unit-f) > 1e-6 {
			return nil, bad(fmt.Sprintf(s.fmtc+" to "+s.fmtc+" in steps of %g", float64(s.lo)*s.unit, float64(s.hi)*s.unit, s.unit))
		}
		return []edit{{at, 0xFF, byte(int8(x))}}, nil
	case kCall:
		b := make([]byte, 8)
		if n != "OFF" {
			c := strings.ToUpper(strings.TrimSpace(v))
			if len(c) > 8 || strings.Trim(c, " ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-/.") != "" {
				return nil, bad("OFF, or up to 8 of A-Z 0-9 - / .")
			}
			copy(b, c)
		}
		var es []edit
		for i, x := range b {
			es = append(es, edit{callAddr + i, 0xFF, x})
		}
		return es, nil
	case kMode:
		return nil, errors.New("MODE is kept for each slot: change it on the radio (MENU, 0 6)")
	case kOffset:
		return nil, errors.New("OFFSET MHz is kept for each band: change it on the radio (MENU, 1 1)")
	}
	return nil, bad("a value")
}

// sameNumber: "100" is 100.0.
func sameNumber(a, b string) bool {
	x, e1 := strconv.ParseFloat(a, 64)
	y, e2 := strconv.ParseFloat(b, 64)
	return e1 == nil && e2 == nil && x == y
}

func withEdits(mem []byte, es []edit) []byte {
	out := slices.Clone(mem)
	for _, e := range es {
		out[e.addr] = out[e.addr]&^e.mask | e.val&e.mask
	}
	return out
}

func showSettings(mem []byte) error {
	if !hasSettings(mem) {
		return fmt.Errorf("no td-h3-fw settings here (check byte %02X): a radio or backup on other firmware", mem[cfgAddr])
	}
	w := 0
	for _, s := range settingsTable {
		w = max(w, len(s.name))
	}
	for _, s := range settingsTable {
		say(noteS.Render(fmt.Sprintf("%2d", s.num)) + "  " + fmt.Sprintf("%-*s", w, s.name) + "  " + valS.Render(s.value(mem)))
	}
	return nil
}

// setWritable: the settings' first block (every menu setting is in it) and the CALL ID's.
func setWritable(addr int) error {
	if addr == cfgAddr || addr == callAddr {
		return nil
	}
	return fmt.Errorf("block %04X is not the settings or the CALL ID: refused", addr)
}

func runSet(portName, name, value string) error {
	s, err := findSetting(name)
	if err != nil {
		return err
	}
	es, err := s.parseValue(value)
	if err != nil {
		return err
	}
	if portName == "" {
		if portName, err = findPort(); err != nil {
			return err
		}
	}
	p, err := openClone(portName)
	if err != nil {
		return err
	}
	defer p.Close()
	path, mem, err := radioBackup(p)
	if err != nil {
		return err
	}
	okLine("backup saved: " + path)
	if !hasSettings(mem) {
		return fmt.Errorf("the radio holds no td-h3-fw settings (check byte %02X): nothing was written", mem[cfgAddr])
	}
	before, after := s.value(mem), s.value(withEdits(mem, es))
	plan := map[int][]edit{}
	for _, e := range es {
		plan[e.addr&^31] = append(plan[e.addr&^31], e)
	}
	n, err := importBlocks(p, plan, setWritable, func(m string, kv ...any) { logger.Debug(m, kv...) })
	if err != nil {
		return fmt.Errorf("the radio's old memory is in %s: %w", path, pcLinkErr(err))
	}
	if n == 0 {
		okLine(s.name + " is already " + after + ": nothing was written")
		return nil
	}
	okLine(s.name + ": " + before + " -> " + after + " (written and checked)")
	if _, cfg := plan[cfgAddr]; !cfg {
		return nil
	}
	if stillInPCLink(p) {
		warnLine("this firmware doesn't restart by itself: switch the radio off and on now, without pressing V/M")
		return nil
	}
	okLine("the radio restarted with it")
	return nil
}
