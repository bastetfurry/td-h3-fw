package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestManualPicture writes the flasher's finished screen for the manual, drawn by the flasher's own
// code (a real flash needs a radio). Only make -C fw screenshots runs it.
func TestManualPicture(t *testing.T) {
	file := os.Getenv("TDH3_SHOT")
	if file == "" {
		t.Skip("make -C fw screenshots runs this")
	}
	img, err := os.ReadFile("../../fw/build/td-h3.bin")
	if err != nil {
		t.Fatal(err)
	}
	ver := os.Getenv("TDH3_SHOT_VER") // the release number alone, as on the radio's pictures
	var b bytes.Buffer
	out = &b
	defer func() { out = os.Stderr; ui = nil }()
	ui = &live{st: stDone, since: time.Now().Add(-time.Second), cap: caption(okS, "v"+ver+" running"), fd: ^uintptr(0)}
	ui.fit(100, 40)
	for range 2 * fallRows * fallEvery { // a full waterfall
		ui.sc.tick(ui.scopeW, 1)
	}
	row("radio", noteS.Render("/dev/ttyUSB0"))
	row("flashing", valS.Render("v"+ver)+"  "+noteS.Render("full (transmit)"))
	row("image", noteS.Render(fmt.Sprintf("td-h3.bin  %d blocks  %.1f KB", (len(img)+31)/32, float64(len(img))/1024)))
	say("")
	okLine("radio found, running " + valS.Render("v0.60.0"))
	okLine("radio ready to restart into flash mode")
	okLine("flash mode")
	okLine("erased")
	okLine(okS.Render("v" + ver + " is running on your radio"))
	say("     " + noteS.Render("(was v0.60.0, 11s, 4.6 KB/s)"))
	ui.draw()
	if err := os.WriteFile(file, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestManualGif writes a whole flash as frames of 100 ms for the manual's GIF, drawn by the
// flasher's own code on a clock of its own: waiting for PTT, erasing, writing, done. Only make -C fw
// screenshots runs it.
func TestManualGif(t *testing.T) {
	dir := os.Getenv("TDH3_GIF")
	if dir == "" {
		t.Skip("make -C fw screenshots runs this")
	}
	img, err := os.ReadFile("../../fw/build/td-h3.bin")
	if err != nil {
		t.Fatal(err)
	}
	ver := os.Getenv("TDH3_SHOT_VER")
	var b bytes.Buffer
	out = &b
	t0 := time.Now()
	at := time.Duration(0) // the flash's own time
	clock = func() time.Time { return t0.Add(at) }
	defer func() { out, clock, ui = os.Stderr, time.Now, nil }()
	ui = &live{cap: caption(ambS, "hold PTT"), fd: ^uintptr(0)}
	ui.fit(100, 40)
	row("radio", noteS.Render("/dev/ttyUSB0"))
	row("flashing", valS.Render("v"+ver)+"  "+noteS.Render("full (transmit)"))
	row("image", noteS.Render(fmt.Sprintf("td-h3.bin  %d blocks  %.1f KB", (len(img)+31)/32, float64(len(img))/1024)))
	say("")
	okLine("radio found, running " + valS.Render("v0.60.0"))
	okLine("radio ready to restart into flash mode")
	n, since := 0, time.Duration(0) // since: when this stage began, on the flash's clock
	shot := func() {
		b.Reset()
		ui.drawn, ui.since = 0, time.Now().Add(since-at)
		ui.sc.tick(ui.scopeW, ui.f) // two scope steps a frame, as at 20 draws a second
		ui.draw()
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d.ans", n)), b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		n++
		at += 100 * time.Millisecond
	}
	stage := func(st stage, cap string) { ui.stage(st, cap); since = at }
	stage(stListen, caption(ambS, "hold PTT"))
	ui.setStatus("press and hold "+ambS.Render("PTT")+" now", true)
	for range 15 {
		shot()
	}
	okLine("flash mode")
	stage(stErase, caption(ambS, "erasing"))
	ui.setStatus("erasing", true)
	for range 10 {
		shot()
	}
	okLine("erased")
	ui.setStatus("", false)
	facts := progressFacts(len(img))
	for i := range 50 { // 10 s at 5.6 KB/s, shown in 5
		f := float64(i) / 50
		ui.write(f, facts(f))
		if i == 0 {
			since = at
		}
		shot()
		at += 100 * time.Millisecond
	}
	ui.write(1, facts(1))
	stage(stDone, caption(okS, "v"+ver+" running"))
	ui.setStatus("", false)
	okLine(okS.Render("v" + ver + " is running on your radio"))
	say("     " + noteS.Render(fmt.Sprintf("(was v0.60.0, 10s, %.1f KB/s)", float64(len(img))/1024/10)))
	for range 15 {
		shot()
	}
}
