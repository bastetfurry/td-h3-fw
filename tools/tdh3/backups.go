// The automatic backups: every command that writes to the radio over PC LINK reads all of it first
// and saves that in backups/ under the date and time. A name is never reused: a second backup in
// the same second gets -2, -3 ... after it.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/spf13/cobra"
)

const backupDir = "backups" // in the current folder

var backupClock = time.Now // a var: tests make two backups in one second

// clonePort is a radio opened for the clone protocol (PC LINK).
type clonePort interface {
	port
	Close() error
}

// openClone opens the radio's port at the clone protocol's 38400. A var: tests stand in for the radio.
var openClone = func(name string) (clonePort, error) { return open(name, 38400) }

// saveNew writes the 8-byte ident and the EEPROM to path, never over a file already there.
func saveNew(path string, ident, mem []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(bytes.Clone(ident), mem...))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// saveBackup saves ident and mem as backups/td-h3-YYYYMMDD-HHMMSS.bin, or -2, -3 ... after it
// when that name is taken. It returns the file's name.
func saveBackup(ident, mem []byte) (string, error) {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	stamp := "td-h3-" + backupClock().Format("20060102-150405")
	for n := 1; ; n++ {
		path := filepath.Join(backupDir, stamp+".bin")
		if n > 1 {
			path = filepath.Join(backupDir, stamp+"-"+strconv.Itoa(n)+".bin")
		}
		switch err := saveNew(path, ident, mem); {
		case err == nil:
			return path, nil
		case !errors.Is(err, fs.ErrExist):
			return "", err
		}
	}
}

// radioBackup reads the whole radio (one clone session, reads only) and saves it with saveBackup.
func radioBackup(p port) (path string, mem []byte, err error) {
	sp := spin("reading the radio")
	ident, mem, err := backup(p, func(int) {})
	sp.stop()
	if err != nil {
		return "", nil, pcLinkErr(err)
	}
	path, err = saveBackup(ident, mem)
	return path, mem, err
}

// saveEEPROM saves an `eeprom backup`: to args[0], never over a file already there unless force,
// or with no name to backups/ under the date and time.
func saveEEPROM(args []string, force bool, ident, mem []byte) (string, error) {
	if len(args) == 0 {
		return saveBackup(ident, mem)
	}
	if force {
		return args[0], os.WriteFile(args[0], append(bytes.Clone(ident), mem...), 0o644)
	}
	if err := saveNew(args[0], ident, mem); errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("%s is already there: give a new name, or --force to replace it", args[0])
	} else if err != nil {
		return "", err
	}
	return args[0], nil
}

// backupInfo is one file in backups/ as tdh3 backups lists it.
type backupInfo struct {
	name, when, firmware, channels string
	at                             time.Time
}

// readBackups lists the files in dir, oldest first: a dated name gives the date and time, else
// the file's own.
func readBackups(dir string) ([]backupInfo, error) {
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []backupInfo
	for _, e := range es {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		b := backupInfo{name: e.Name(), at: fi.ModTime(), firmware: "-", channels: "-"}
		if m := datedName.FindStringSubmatch(e.Name()); m != nil {
			if t, err := time.ParseInLocation("20060102-150405", m[1], time.Local); err == nil {
				b.at = t
			}
		}
		b.when = b.at.Format("2006-01-02 15:04:05")
		if mem, err := loadImage(filepath.Join(dir, e.Name())); err != nil {
			b.firmware = "not a TD-H3 backup"
		} else {
			if slices.Contains(settingsMagics, mem[cfgAddr]) {
				b.firmware = "td-h3-fw"
			}
			b.channels = strconv.Itoa(len(usedChannels(mem)))
		}
		out = append(out, b)
	}
	slices.SortStableFunc(out, func(a, b backupInfo) int { return a.at.Compare(b.at) })
	return out, nil
}

var datedName = regexp.MustCompile(`^td-h3-(\d{8}-\d{6})(-\d+)?\.bin$`)

func backupsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backups",
		Short: "List the backups in the backups folder (date, firmware, channels)",
		Long: "Lists the files in backups/ inside the current folder: where tdh3 saves a backup by itself\n" +
			"before every change, and where tdh3 eeprom backup puts one given no name. For each: the date\n" +
			"and time it was made, td-h3-fw when it holds this firmware's settings (a backup never records\n" +
			"the version number), and how many memory channels it holds. Doesn't need a radio.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			bs, err := readBackups(backupDir)
			if err != nil {
				return err
			}
			if len(bs) == 0 {
				say(noteS.Render("no backups in " + backupDir + string(filepath.Separator) + " here yet: tdh3 eeprom backup makes one"))
				return nil
			}
			t := table.New().Border(lipgloss.NormalBorder()).Headers("File", "Made", "Firmware", "Channels")
			for _, b := range bs {
				t.Row(filepath.Join(backupDir, b.name), b.when, b.firmware, b.channels)
			}
			lipgloss.Println(t)
			return nil
		},
	}
}
