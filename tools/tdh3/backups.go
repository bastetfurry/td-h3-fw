// The automatic backups: every command that writes to the radio over PC LINK reads all of it first
// and saves that in backups/ under the date and time. A name is never reused: a second backup in
// the same second gets -2, -3 ... after it.
package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
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
