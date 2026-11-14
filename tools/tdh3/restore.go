// tdh3 restore: a whole `tdh3 eeprom backup` put back in one step: the channels, the FM presets,
// the settings, the CALL ID and the stored DTMF codes. It is channels copy's session (copySession)
// with more spans: only their bytes can change, every other byte of the block goes back as the
// radio has it, and the factory calibration is in none of them.
//
// Where each lives, from fw/: the settings are cfg at 0x1FC0-0x1FEF (main.c CFG_ADDR, cfg_t); the
// CALL ID is 8 bytes at 0x1E00 (main.c CALL_ADDR, read by radio.c name_read); DTMF code n is 7
// bytes at 0x1830 + 16 (n - 1) (main.c code_load). Channels and FM presets: chcopy.go.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const (
	callAddr = 0x1E00 // CALL ID: 8 characters, blank (FF or 00) is OFF
	dtmfAddr = 0x1830 // DTMF code n at dtmfAddr + 16 (n - 1), 7 digits
	dtmfLen  = 7
)

var (
	settingsSpan = span{cfgAddr, 0x1FF0, 0xFF, "every menu setting, both slots, the FM radio station"}
	callSpan     = span{callAddr, callAddr + 8, 0xFF, "CALL ID"}
)

// restoreSpans are the bytes tdh3 restore puts back: the channels, the FM presets, the settings,
// the CALL ID and the 8 DTMF codes.
func restoreSpans() []span {
	s := append(spansFor(true), settingsSpan, callSpan)
	for n := range 8 {
		a := dtmfAddr + 16*n
		s = append(s, span{a, a + dtmfLen, 0xFF, "DTMF code " + strconv.Itoa(n+1)})
	}
	return s
}

// spanDiffers says whether any copied bit of s differs between a and b.
func spanDiffers(a, b []byte, s span) bool {
	for i := s.from; i < s.to; i++ {
		m := byte(0xFF)
		if i == s.to-1 {
			m = s.last
		}
		if (a[i]^b[i])&m != 0 {
			return true
		}
	}
	return false
}

// callID is the CALL ID as the radio reads it (name_read: printable characters up to the first other byte).
func callID(mem []byte) string {
	b := mem[callAddr : callAddr+8]
	for i, c := range b {
		if c < 0x20 || c > 0x7E {
			return string(b[:i])
		}
	}
	return string(b)
}

func offOr(s string) string {
	if s == "" {
		return "OFF"
	}
	return s
}

// dtmfCode is stored code n (1-8) as text, "" for a blank one (code_load: seven 0s, or an FF first).
func dtmfCode(mem []byte, n int) string {
	b := mem[dtmfAddr+16*(n-1) : dtmfAddr+16*(n-1)+dtmfLen]
	if bytes.Equal(b, make([]byte, dtmfLen)) {
		return ""
	}
	var s []byte
	for _, d := range b {
		if d >= 16 {
			break
		}
		s = append(s, "0123456789ABCD*#"[d])
	}
	return string(s)
}

// chanDiff lists the channels only in to (added), only in from (removed), and in both but not the same (changed).
func chanDiff(from, to []byte) (added, removed, changed []int) {
	for n := 1; n <= maxChan; n++ {
		a, b := bitOn(from, usedBase, n), bitOn(to, usedBase, n)
		switch {
		case b && !a:
			added = append(added, n)
		case a && !b:
			removed = append(removed, n)
		case a && b && (!bytes.Equal(from[0x10*n:0x10*n+16], to[0x10*n:0x10*n+16]) ||
			!bytes.Equal(from[nameBase+8*(n-1):nameBase+8*n], to[nameBase+8*(n-1):nameBase+8*n]) ||
			bitOn(from, scanBase, n) != bitOn(to, scanBase, n)):
			changed = append(changed, n)
		}
	}
	return
}

// fmDiff lists the FM presets (1-25) that differ.
func fmDiff(from, to []byte) (ns []int) {
	for n := 1; n <= 25; n++ {
		a := 0x0CD0 + 4*(n-1)
		if !bytes.Equal(from[a:a+4], to[a:a+4]) || bitOn(from, 0x1940, n) != bitOn(to, 0x1940, n) {
			ns = append(ns, n)
		}
	}
	return
}

// numbers is "3, 11, 40 and 2 more".
func numbers(ns []int) string {
	var s []string
	for _, n := range ns[:min(len(ns), 8)] {
		s = append(s, strconv.Itoa(n))
	}
	if len(ns) > 8 {
		return strings.Join(s, ", ") + fmt.Sprintf(" and %d more", len(ns)-8)
	}
	return strings.Join(s, ", ")
}

// restoreLines says what tdh3 restore would change on a radio holding mem; none: nothing.
func restoreLines(mem, src []byte) [][2]string {
	var out [][2]string
	add := func(k, v string) { out = append(out, [2]string{k, v}) }
	added, removed, changed := chanDiff(mem, src)
	if len(added) > 0 {
		add("adds", plural(len(added))+": "+numbers(added))
	}
	if len(removed) > 0 {
		add("removes", plural(len(removed))+": "+numbers(removed))
	}
	if len(changed) > 0 {
		add("changes", plural(len(changed))+": "+numbers(changed))
	}
	if fm := fmDiff(mem, src); len(fm) > 0 {
		add("FM", fmt.Sprintf("%d presets: %s", len(fm), numbers(fm)))
	}
	if spanDiffers(mem, src, settingsSpan) {
		add("settings", "the menu settings, both slots and the FM radio station, as in the backup")
	}
	if a, b := callID(mem), callID(src); a != b {
		add("CALL ID", offOr(a)+" -> "+offOr(b))
	}
	for n := 1; n <= 8; n++ {
		if a, b := dtmfCode(mem, n), dtmfCode(src, n); a != b {
			add("DTMF "+strconv.Itoa(n), offOr(a)+" -> "+offOr(b))
		}
	}
	return out
}

func runWholeRestore(portName, file string, ask bool) error {
	src, err := loadImage(file)
	if err != nil {
		return err
	}
	if !slices.Contains(settingsMagics, src[cfgAddr]) {
		return fmt.Errorf("%s holds no td-h3-fw settings (check byte %02X): nothing was written; "+
			"tdh3 channels copy puts back only its channels", file, src[cfgAddr])
	}
	if portName == "" {
		if portName, err = findPort(); err != nil {
			return err
		}
	}
	if ask && !isTerm(os.Stdin) {
		return errors.New("not a terminal to press Enter in: pass --yes")
	}
	p, err := openClone(portName)
	if err != nil {
		return err
	}
	defer p.Close()
	sp := spin("reading the radio")
	ident, mem, err := backup(p, func(int) {})
	sp.stop()
	if err != nil {
		return pcLinkErr(err)
	}
	if !tdh3Ident(ident) {
		return fmt.Errorf("the connected radio says %q, not a TD-H3: nothing written", ident)
	}
	lines := restoreLines(mem, src)
	say("")
	row("radio", valS.Render(portName))
	row("from", valS.Render(filepath.Base(file)))
	if len(lines) == 0 {
		okLine("the radio already holds everything in this backup: nothing was written")
		return nil
	}
	for _, l := range lines {
		row(l[0], l[1])
	}
	row("never", "the factory TX calibration")
	row("backup", "the whole radio first, into "+valS.Render(backupDir+string(filepath.Separator)))
	if ask {
		end := watchStop()
		err := enter(ambS.Render("Enter") + " restores" + noteS.Render(", Ctrl-C stops"))
		end()
		if err != nil {
			return err
		}
	}
	spans := restoreSpans()
	sp = spin("reading the radio")
	var path string
	written, err := copySession(p, copyPlan(src, spans), spans, func(ident, mem []byte) (err error) {
		if path, err = saveBackup(ident, mem); err == nil {
			sp.set("writing the backup to the radio")
		}
		return err
	})
	sp.stop()
	if path != "" {
		okLine("backup saved: " + path)
	}
	if err != nil && written > 0 {
		return fmt.Errorf("stopped part-way, the radio may hold a mix of old and new (run tdh3 restore again; its old memory is in %s): %w", path, err)
	}
	if err != nil {
		return pcLinkErr(err)
	}
	okLine(fmt.Sprintf("restored and checked (%d blocks written)", written))
	if !spanDiffers(mem, src, settingsSpan) {
		return nil
	}
	if stillInPCLink(p) {
		warnLine("this firmware doesn't restart by itself: switch the radio off and on now, without pressing V/M")
		return nil
	}
	okLine("the radio restarted with the restored settings")
	return nil
}
