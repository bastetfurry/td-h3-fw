package main

import (
	"bytes"
	"strings"
	"testing"
)

func cfgMem() []byte {
	mem := seededEEPROM()
	copy(mem[cfgAddr:cfgAddr+48], make([]byte, 48))
	mem[cfgAddr] = 0xAD
	return mem
}

// Every value the table can show, typed back in, sets exactly that value.
func TestSetTakesEveryValueTheMenuShows(t *testing.T) {
	values := func(s setting) []string {
		var out []string
		switch s.kind {
		case kList:
			for x := range s.n {
				if s.labels == nil {
					out = append(out, levelText(x))
				} else {
					out = append(out, s.labels[x])
				}
			}
		case kLevel, kVox:
			for x := range s.n {
				out = append(out, levelText(x))
			}
			if s.kind == kVox {
				out[0] = "OFF"
			}
		case kTxb:
			for x := range s.n {
				out = append(out, txbText(x))
			}
		case kWx:
			for x := range s.n {
				out = append(out, wxText(x))
			}
		case kTone:
			for i := range nToneMenu {
				out = append(out, toneText(toneIndex(i)))
			}
		case kGroup:
			for g := range 256 {
				out = append(out, groupText(byte(g)))
			}
		case kPrio:
			out = append(out, "OFF", "CH001", "CH199")
		case kCal:
			for x := s.lo; x <= s.hi; x++ {
				mem := cfgMem()
				mem[cfgAddr+s.off] = byte(int8(x))
				out = append(out, s.value(mem))
			}
		case kCall:
			out = append(out, "OFF", "N0CALL", "AB1CD/P", "K-1.")
		}
		return out
	}
	for _, s := range settingsTable {
		for _, v := range values(s) {
			es, err := s.parseValue(v)
			if s.kind == kMode || s.kind == kOffset {
				if err == nil {
					t.Errorf("%s: set although it is kept per slot or band", s.name)
				}
				break
			}
			if err != nil {
				t.Errorf("%s %q: %v", s.name, v, err)
				continue
			}
			mem := cfgMem()
			got := withEdits(mem, es)
			if s.value(got) != v {
				t.Errorf("%s: set %q, reads back %q", s.name, v, s.value(got))
			}
			for i := range mem { // nothing but this setting's bytes
				if got[i] != mem[i] && setWritable(i&^31) != nil {
					t.Fatalf("%s: changed byte %04X", s.name, i)
				}
			}
		}
	}
}

func TestSetUnderstandsLooseTyping(t *testing.T) {
	for _, c := range [][3]string{
		{"power", "high", "HIGH"}, {"scanpause", "5s", "5 s"}, {"22", "busy", "BUSY"}, {"TX TIMEOUT", "30 s", "030 s"},
		{"RX TONE", "100", "100.0"}, {"tx tone", "d023i", "D023I"}, {"TX BANDS", "ham,gmrs", "HAM GMRS"},
		{"WX ALERT", "WX3", "162.450"}, {"SCAN GROUP", "1,3", "G13"}, {"PRIORITY", "11", "CH011"},
		{"FREQ CAL", "-1.5", "-1.5 ppm"}, {"BATT CAL", "0.1 V", "+0.10 V"}, {"call id", "n0call", "N0CALL"},
	} {
		s, err := findSetting(c[0])
		must(t, err)
		es, err := s.parseValue(c[1])
		if err != nil {
			t.Errorf("%s %q: %v", c[0], c[1], err)
			continue
		}
		if got := s.value(withEdits(cfgMem(), es)); got != c[2] {
			t.Errorf("%s %q reads %q, want %q", c[0], c[1], got, c[2])
		}
	}
	for _, c := range [][2]string{{"POWER", "huge"}, {"SQUELCH", "S10"}, {"FREQ CAL", "0.05"}, {"METER CAL", "41"},
		{"PRIORITY", "200"}, {"CALL ID", "TOOLONGNAME"}, {"CALL ID", "A_B"}, {"RX TONE", "101.0"}, {"MODE", "AM"}, {"OFFSET", "0.6"}} {
		if s, err := findSetting(c[0]); err == nil {
			if _, err := s.parseValue(c[1]); err == nil {
				t.Errorf("%s %q accepted", c[0], c[1])
			}
		}
	}
	if _, err := findSetting("SCOPE"); err == nil {
		t.Error("SCOPE taken as a setting")
	}
}

func TestSettingsSetBacksUpWritesAndRestarts(t *testing.T) {
	inTemp(t)
	seed := cfgMem()
	mem := bytes.Clone(seed)
	f := restartingRadio(t, mem, false)
	openFake(t, f)
	must(t, runSet("fake", "POWER", "high"))
	checkBackupFirst(t, seed)
	want := bytes.Clone(seed)
	want[cfgAddr+3] = seed[cfgAddr+3]&^3 | 3
	if !bytes.Equal(mem, want) {
		t.Fatal("the EEPROM changed beyond POWER's bits")
	}
}

func TestSettingsSetCallIDLeavesTheSettings(t *testing.T) {
	inTemp(t)
	seed := cfgMem()
	mem := bytes.Clone(seed)
	f, writes := cloneRadio(t, mem)
	openFake(t, f)
	must(t, runSet("fake", "CALL ID", "N0CALL"))
	if len(*writes) != 1 || (*writes)[0] != callAddr || callID(mem) != "N0CALL" || !bytes.Equal(mem[cfgAddr:], seed[cfgAddr:]) {
		t.Fatalf("wrote %04X; CALL ID %q", *writes, callID(mem))
	}
}

func TestSettingsSetRefusesARadioWithoutSettings(t *testing.T) {
	inTemp(t)
	f, writes := cloneRadio(t, seededEEPROM()) // check byte 0x3B: not td-h3-fw's
	openFake(t, f)
	if err := runSet("fake", "POWER", "HIGH"); err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v", err)
	}
	if len(*writes) != 0 {
		t.Fatal("written")
	}
}
