package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// openFake makes the clone commands talk to f instead of a serial port.
func openFake(t *testing.T, f *fake) {
	t.Helper()
	old := openClone
	openClone = func(string) (clonePort, error) { return closer{f}, nil }
	t.Cleanup(func() { openClone = old })
}

type closer struct{ *fake }

func (closer) Close() error { return nil }

// inTemp runs the test in an empty folder, so backups/ is its own.
func inTemp(t *testing.T) string {
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// backupsOnDisk is the files in backups/, sorted.
func backupsOnDisk(t *testing.T) []string {
	t.Helper()
	es, _ := os.ReadDir(backupDir)
	var out []string
	for _, e := range es {
		out = append(out, filepath.Join(backupDir, e.Name()))
	}
	return out
}

func TestSaveBackupNeverReplacesAFile(t *testing.T) {
	inTemp(t)
	old := backupClock
	backupClock = func() time.Time { return time.Date(2026, 11, 14, 15, 30, 12, 0, time.Local) }
	t.Cleanup(func() { backupClock = old })
	ident := []byte("P31183\xff\xff")
	var got []string
	for i := range 3 {
		p, err := saveBackup(ident, bytes.Repeat([]byte{byte(i)}, 0x2000))
		must(t, err)
		got = append(got, p)
	}
	want := []string{"backups/td-h3-20261114-153012.bin", "backups/td-h3-20261114-153012-2.bin", "backups/td-h3-20261114-153012-3.bin"}
	for i := range want {
		want[i] = filepath.FromSlash(want[i])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("names %q, want %q", got, want)
	}
	for i, p := range got { // each file still holds what was saved under its name
		mem, err := loadImage(p)
		must(t, err)
		if mem[0] != byte(i) {
			t.Errorf("%s was replaced", p)
		}
	}
}

// checkBackupFirst says the one backup on disk is the radio as it was before the command.
func checkBackupFirst(t *testing.T, before []byte) {
	t.Helper()
	files := backupsOnDisk(t)
	if len(files) != 1 {
		t.Fatalf("backups: %q, want one", files)
	}
	mem, err := loadImage(files[0])
	must(t, err)
	if !bytes.Equal(mem, before) {
		t.Fatal("the backup is not the radio as it was before the write")
	}
}

func TestResetSavesABackupFirst(t *testing.T) {
	inTemp(t)
	seed := seededEEPROM()
	seed[0x1FC0] = 0xAD
	mem := bytes.Clone(seed)
	openFake(t, restartingRadio(t, mem, false))
	must(t, runReset("fake", false, false))
	checkBackupFirst(t, seed)
	if mem[0x1FC0] != 0 {
		t.Fatal("settings not reset")
	}
}

func TestSettingsRestoreSavesABackupFirst(t *testing.T) {
	dir := inTemp(t)
	seed := seededEEPROM()
	src := bytes.Clone(seed)
	src[0x1FC0], src[0x1FC1] = 0xAD, seed[0x1FC1]^0xFF
	file := filepath.Join(dir, "other.bin")
	must(t, os.WriteFile(file, append([]byte("P31183\xff\xff"), src...), 0o644))
	mem := bytes.Clone(seed)
	openFake(t, restartingRadio(t, mem, false))
	must(t, runRestore("fake", file, false))
	checkBackupFirst(t, seed)
	if mem[0x1FC1] != src[0x1FC1] {
		t.Fatal("settings not restored")
	}
}

func TestImportSavesABackupFirst(t *testing.T) {
	dir := inTemp(t)
	file := filepath.Join(dir, "list.csv")
	must(t, os.WriteFile(file, []byte(threeRows), 0o644))
	seed := seededEEPROM()
	mem := bytes.Clone(seed)
	f, _ := cloneRadio(t, mem)
	openFake(t, f)
	must(t, runImport("fake", file, false, false))
	checkBackupFirst(t, seed)
	if chanName(mem, 1) != "CALL" {
		t.Fatal("channels not imported")
	}
}

func TestBackupFailureWritesNothing(t *testing.T) {
	inTemp(t)
	must(t, os.WriteFile(backupDir, nil, 0o644)) // a file where the folder should be
	f, writes := cloneRadio(t, seededEEPROM())
	openFake(t, f)
	if err := runReset("fake", true, false); err == nil || !strings.Contains(err.Error(), backupDir) {
		t.Fatalf("err = %v, want the backup's error", err)
	}
	if len(*writes) != 0 {
		t.Fatalf("%d blocks written without a backup", len(*writes))
	}
}

func TestEEPROMBackupNeverReplacesWithoutForce(t *testing.T) {
	dir := inTemp(t)
	ident, mem := []byte("P31183\xff\xff"), seededEEPROM()
	file := filepath.Join(dir, "my-radio.bin")
	must(t, os.WriteFile(file, []byte("keep me"), 0o644))
	if _, err := saveEEPROM([]string{file}, false, ident, mem); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a refusal naming --force", err)
	}
	if b, _ := os.ReadFile(file); string(b) != "keep me" {
		t.Fatal("the file was replaced")
	}
	_, err := saveEEPROM([]string{file}, true, ident, mem)
	must(t, err)
	if got, err := loadImage(file); err != nil || !bytes.Equal(got, mem) {
		t.Fatalf("--force didn't replace it (%v)", err)
	}
	p, err := saveEEPROM(nil, false, ident, mem)
	must(t, err)
	if !datedName.MatchString(filepath.Base(p)) || filepath.Dir(p) != backupDir {
		t.Fatalf("no name saved as %s, want backups/td-h3-DATE-TIME.bin", p)
	}
}

func TestBackupsListsDateFirmwareAndChannels(t *testing.T) {
	inTemp(t)
	if bs, err := readBackups(backupDir); err != nil || len(bs) != 0 {
		t.Fatalf("no folder: %v %v", bs, err)
	}
	must(t, os.Mkdir(backupDir, 0o755))
	ours := blank()
	ours[cfgAddr] = 0xAD
	add(ours, channel{loc: 1, rx: 14652000, tx: 14652000, rxTone: noTone, txTone: noTone}, channel{loc: 5, rx: 14652000, tx: 14652000, rxTone: noTone, txTone: noTone})
	put := func(name string, b []byte) { must(t, os.WriteFile(filepath.Join(backupDir, name), b, 0o644)) }
	put("td-h3-20261114-153012-2.bin", append([]byte("P31183\xff\xff"), ours...))
	stock := make([]byte, 0x2000) // no channels, no td-h3-fw settings
	put("td-h3-20261101-090000.bin", append([]byte("P31183\xff\xff"), stock...))
	put("notes.txt", []byte("hello"))
	old := time.Date(2025, 1, 2, 3, 4, 5, 0, time.Local)
	must(t, os.Chtimes(filepath.Join(backupDir, "notes.txt"), old, old))
	bs, err := readBackups(backupDir)
	must(t, err)
	var got []string
	for _, b := range bs {
		got = append(got, strings.Join([]string{b.name, b.when, b.firmware, b.channels}, "|"))
	}
	want := []string{
		"notes.txt|2025-01-02 03:04:05|not a TD-H3 backup|-",
		"td-h3-20261101-090000.bin|2026-11-01 09:00:00|-|0",
		"td-h3-20261114-153012-2.bin|2026-11-14 15:30:12|td-h3-fw|2",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
