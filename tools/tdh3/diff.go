// tdh3 diff: what differs between two backups, or a backup and the radio. Reads only.
package main

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// chanFields is a channel's fields by name, as channels list shows them.
func chanFields(c channel) [][2]string {
	bw, scan := "wide", "no"
	if c.narrow {
		bw = "narrow"
	}
	if c.scan {
		scan = "yes"
	}
	return [][2]string{{"name", c.name}, {"RX MHz", mhz(c.rx)}, {"TX MHz", mhz(c.tx)}, {"RX tone", c.rxTone.String()},
		{"TX tone", c.txTone.String()}, {"power", powerNames[c.power]}, {"bandwidth", bw},
		{"mode", modeS[min(int(c.mode), 2)]}, {"scan", scan}, {"groups", groupList(c.groups)}}
}

func chanShort(c channel) string { return strings.TrimSpace(fmt.Sprintf("%-8s %s", c.name, mhz(c.rx))) }

// fmPreset is preset n (1-25) as the FM radio shows it, "" when empty.
func fmPreset(mem []byte, n int) string {
	a := 0x0CD0 + 4*(n-1)
	v := unbcd(binary.LittleEndian.Uint32(mem[a:]))
	if !bitOn(mem, 0x1940, n) || mem[a] == 0xFF || v < 760 || v > 1080 {
		return ""
	}
	return fmt.Sprintf("%d.%d", v/10, v%10)
}

// diffSection is a heading and its lines, each a label and what changed.
type diffSection struct {
	title string
	lines [][2]string
}

// diffMem lists what differs from a to b.
func diffMem(a, b []byte) []diffSection {
	var out []diffSection
	add := func(title string, lines [][2]string) {
		if len(lines) > 0 {
			out = append(out, diffSection{title, lines})
		}
	}
	added, removed, changed := chanDiff(a, b)
	var ch [][2]string
	for _, n := range removed {
		ch = append(ch, [2]string{strconv.Itoa(n), "removed (was " + chanShort(decode(a, n)) + ")"})
	}
	for _, n := range added {
		ch = append(ch, [2]string{strconv.Itoa(n), "added: " + chanShort(decode(b, n))})
	}
	for _, n := range changed {
		fa, fb := chanFields(decode(a, n)), chanFields(decode(b, n))
		var what []string
		for i := range fa {
			if fa[i][1] != fb[i][1] {
				what = append(what, fa[i][0]+" "+offOr2(fa[i][1])+" -> "+offOr2(fb[i][1]))
			}
		}
		if what == nil {
			what = []string{"other bits of its record"}
		}
		ch = append(ch, [2]string{strconv.Itoa(n), strings.Join(what, ", ")})
	}
	add("channels", ch)
	var fm [][2]string
	for _, n := range fmDiff(a, b) {
		if x, y := fmPreset(a, n), fmPreset(b, n); x != y {
			fm = append(fm, [2]string{strconv.Itoa(n), emptyOr(x) + " -> " + emptyOr(y)})
		}
	}
	add("FM presets", fm)
	var st [][2]string
	switch ha, hb := hasSettings(a), hasSettings(b); {
	case ha && hb:
		for _, s := range settingsTable {
			if x, y := s.value(a), s.value(b); x != y {
				st = append(st, [2]string{s.name, x + " -> " + y})
			}
		}
		oa, ob := otherSettings(a), otherSettings(b)
		for i := range oa {
			if oa[i][1] != ob[i][1] {
				st = append(st, [2]string{oa[i][0], oa[i][1] + " -> " + ob[i][1]})
			}
		}
	case ha != hb:
		which := "the first"
		if hb {
			which = "the second"
		}
		st = append(st, [2]string{"", "only " + which + " holds td-h3-fw settings"})
	}
	if x, y := callID(a), callID(b); x != y && !(hasSettings(a) && hasSettings(b)) { // else in the table
		st = append(st, [2]string{"CALL ID", offOr(x) + " -> " + offOr(y)})
	}
	for n := 1; n <= 8; n++ {
		if x, y := dtmfCode(a, n), dtmfCode(b, n); x != y {
			st = append(st, [2]string{"DTMF code " + strconv.Itoa(n), offOr(x) + " -> " + offOr(y)})
		}
	}
	add("settings", st)
	return out
}

func offOr2(s string) string {
	if s == "" {
		return "(blank)"
	}
	return s
}

func emptyOr(s string) string {
	if s == "" {
		return "empty"
	}
	return s
}

func printDiff(nameA, nameB string, d []diffSection) {
	say("")
	row("from", valS.Render(nameA))
	row("to", valS.Render(nameB))
	if len(d) == 0 {
		okLine("the same: no channel, FM preset or setting differs")
		return
	}
	for _, s := range d {
		say("")
		say(ambS.Render(s.title))
		w := 0
		for _, l := range s.lines {
			w = max(w, len(l[0]))
		}
		for _, l := range s.lines {
			say("  " + noteS.Render(fmt.Sprintf("%-*s", w, l[0])) + "  " + l[1])
		}
	}
}

func diffCmd(port *string) *cobra.Command {
	return &cobra.Command{
		Use:   "diff A.bin [B.bin]",
		Short: "Show what differs between two backups, or a backup and the radio (reads only)",
		Long: "Lists the channels added, removed and changed (which fields), the FM radio presets, and the\n" +
			"settings by their menu name, the CALL ID and the DTMF codes, each as A's value -> B's. With one\n" +
			"backup it compares it with the radio, which must be in PC LINK (MENU, 4 6, MENU): what changed\n" +
			"on the radio since the backup. Nothing is ever written.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			a, err := loadImage(args[0])
			if err != nil {
				return err
			}
			nameB := "the radio"
			var b []byte
			if len(args) == 2 {
				nameB = filepath.Base(args[1])
				b, err = loadImage(args[1])
			} else {
				b, err = readMem(*port, "")
			}
			if err != nil {
				return err
			}
			printDiff(filepath.Base(args[0]), nameB, diffMem(a, b))
			return nil
		},
	}
}
