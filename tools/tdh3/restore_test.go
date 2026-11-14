package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreSpansMissTheCalibration(t *testing.T) {
	for _, s := range restoreSpans() {
		if s.from < 0x1F80 && s.to > 0x1F50 || s.to > 0x1FF0 {
			t.Errorf("span %04X-%04X (%s) reaches the calibration", s.from, s.to-1, s.what)
		}
	}
}

// restoreBits are the bits tdh3 restore puts back, written out by hand from fw/: channels copy's
// (copiedBits), the settings 1FC0-1FEF, CALL ID 1E00-1E07, DTMF codes 1830+16n, 7 bytes each.
func restoreBits(a int) byte {
	switch {
	case a >= 0x1FC0 && a < 0x1FF0, a >= 0x1E00 && a < 0x1E08:
		return 0xFF
	case a >= 0x1830 && a < 0x18B0 && (a-0x1830)%16 < 7:
		return 0xFF
	}
	return copiedBits(a, true)
}

func writeBackup(t *testing.T, dir string, mem []byte) string {
	t.Helper()
	p := filepath.Join(dir, "my-radio.bin")
	must(t, os.WriteFile(p, append([]byte("P31183\xff\xff"), mem...), 0o644))
	return p
}

func TestRestorePutsBackTheWholeBackup(t *testing.T) {
	dir := inTemp(t)
	src, seed := srcEEPROM(), seededEEPROM()
	src[cfgAddr] = 0xAD
	file := writeBackup(t, dir, src)
	mem := bytes.Clone(seed)
	openFake(t, restartingRadio(t, mem, false))
	must(t, runWholeRestore("fake", file, false))
	for a := range mem {
		m := restoreBits(a)
		if want := seed[a]&^m | src[a]&m; mem[a] != want {
			t.Fatalf("byte %04X = %02X, want %02X (restored mask %02X)", a, mem[a], want, m)
		}
	}
	checkBackupFirst(t, seed)
}

func TestWholeRestoreRefusesABackupWithoutSettings(t *testing.T) {
	dir := inTemp(t)
	src := srcEEPROM()
	src[cfgAddr] = 0xFF // a stock radio's
	f, writes := cloneRadio(t, seededEEPROM())
	openFake(t, f)
	if err := runWholeRestore("fake", writeBackup(t, dir, src), false); err == nil {
		t.Fatal("a backup with no td-h3-fw settings was restored")
	}
	if len(*writes) != 0 || len(f.sent) != 0 {
		t.Fatal("the radio was touched")
	}
}

func TestRestoreOfTheSameRadioWritesNothing(t *testing.T) {
	dir := inTemp(t)
	seed := seededEEPROM()
	seed[cfgAddr] = 0xAD
	f, writes := cloneRadio(t, bytes.Clone(seed))
	openFake(t, f)
	must(t, runWholeRestore("fake", writeBackup(t, dir, seed), false))
	if len(*writes) != 0 || len(backupsOnDisk(t)) != 0 {
		t.Fatalf("%d blocks written, backups %q", len(*writes), backupsOnDisk(t))
	}
}

func TestRestoreLinesSayWhatChanges(t *testing.T) {
	radio, src := blank(), blank()
	add(radio, channel{loc: 1, name: "A", rx: 14652000, tx: 14652000, rxTone: noTone, txTone: noTone}, channel{loc: 2, name: "B", rx: 14652000, tx: 14652000, rxTone: noTone, txTone: noTone})
	add(src, channel{loc: 2, name: "BB", rx: 14652000, tx: 14652000, rxTone: noTone, txTone: noTone}, channel{loc: 3, name: "C", rx: 14652000, tx: 14652000, rxTone: noTone, txTone: noTone})
	copy(src[callAddr:], "N0CALL\x00")
	copy(src[dtmfAddr+16:], []byte{1, 2, 3, 0xFF, 0xFF, 0xFF, 0xFF})
	got := map[string]string{}
	for _, l := range restoreLines(radio, src) {
		got[l[0]] = l[1]
	}
	want := map[string]string{"adds": "1 channel: 3", "removes": "1 channel: 1", "changes": "1 channel: 2",
		"CALL ID": "OFF -> N0CALL", "DTMF 2": "OFF -> 123"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("lines %q", got)
	}
}
