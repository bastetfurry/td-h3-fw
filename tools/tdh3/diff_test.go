package main

import (
	"bytes"
	"strings"
	"testing"
)

func flat(d []diffSection) string {
	var out []string
	for _, s := range d {
		for _, l := range s.lines {
			out = append(out, s.title+"|"+l[0]+"|"+l[1])
		}
	}
	return strings.Join(out, "\n")
}

func TestDiffListsWhatChanged(t *testing.T) {
	a := blank()
	copy(a[cfgAddr:], make([]byte, 48))
	a[cfgAddr] = 0xAD
	a[0x1940], a[0x1941], a[0x1942], a[0x1943] = 0, 0, 0, 0
	c := func(loc int, name string, rx uint32) channel {
		return channel{loc: loc, name: name, rx: rx, tx: rx, rxTone: noTone, txTone: noTone, scan: true}
	}
	add(a, c(1, "CALL", 14652000), c(2, "RPTR", 14694000), c(3, "OLD", 14600000))
	b := bytes.Clone(a)
	for i := 0x1900; i < 0x1919; i++ {
		b[i] = 0
	}
	x := c(2, "RPTR", 14694000)
	x.tx, x.power, x.rxTone = 14634000, powerHigh, 0x1000
	add(b, c(1, "CALL", 14652000), x, c(4, "NEW", 44600000))
	copy(b[0x0CD0:], []byte{0x19, 0x10, 0, 0}) // FM preset 1: 101.9
	b[0x1940] = 1
	b[cfgAddr+3] = 3 // POWER HIGH
	b[cfgAddr+13] = 1
	copy(b[callAddr:], "N0CALL\x00\x00")
	want := strings.Join([]string{
		"channels|3|removed (was OLD      146.00000)",
		"channels|4|added: NEW      446.00000",
		"channels|2|TX MHz 146.94000 -> 146.34000, RX tone - -> 100.0, power Low -> High",
		"FM presets|1|empty -> 101.9",
		"settings|POWER|ULTRA LOW -> HIGH",
		"settings|CALL ID|OFF -> N0CALL",
		"settings|selected slot|A -> B",
	}, "\n")
	if got := flat(diffMem(a, b)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if d := diffMem(a, a); len(d) != 0 {
		t.Fatalf("a backup differs from itself: %s", flat(d))
	}
}
