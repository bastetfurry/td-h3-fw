package main

import (
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fwSource is fw/main.c with its comments taken out.
func fwSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../fw/" + name)
	if err != nil {
		t.Fatalf("the firmware source is needed for this check: %v", err)
	}
	s := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	return regexp.MustCompile(`//[^\n]*`).ReplaceAllString(s, "")
}

func cut(t *testing.T, src, re string) string {
	t.Helper()
	m := regexp.MustCompile(re).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("fw/main.c no longer has %s: update this check with the firmware", re)
	}
	return m[1]
}

func fields(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// cfgFields lays out cfg_t as SDCC does: a line of bit-fields shares one byte (filled from bit 0),
// every other field its own bytes. It returns each named field's byte, and its bit and width.
func cfgFields(t *testing.T, src string) map[string][3]int {
	body := cut(t, src, `(?s)typedef struct \{\s*(uint8_t magic.*?)\} cfg_t;`)
	size := map[string]int{"uint8_t": 1, "int8_t": 1, "uint16_t": 2, "uint32_t": 4, "slot_t": 6}
	out, at := map[string][3]int{}, 0
	for _, line := range strings.Split(body, ";") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		typ, rest, _ := strings.Cut(line, " ")
		n, ok := size[typ]
		if !ok {
			t.Fatalf("cfg_t: unknown type in %q", line)
		}
		if strings.Contains(rest, ":") {
			bit := 0
			for _, f := range fields(rest) {
				name, w, _ := strings.Cut(f, ":")
				width, _ := strconv.Atoi(strings.TrimSpace(w))
				if name = strings.TrimSpace(name); name != "" {
					out[name] = [3]int{at, bit, width}
				}
				bit += width
			}
			if bit > 8 {
				t.Fatalf("cfg_t: %q is more than a byte", line)
			}
			at++
			continue
		}
		for _, f := range fields(rest) {
			k := 1
			if m := regexp.MustCompile(`(\w+)\[(\d+)\]`).FindStringSubmatch(f); m != nil {
				f = m[1]
				k, _ = strconv.Atoi(m[2])
			}
			out[f] = [3]int{at, 0, 8 * n}
			at += n * k
		}
	}
	if at > 48 {
		t.Fatalf("cfg_t is %d bytes", at)
	}
	return out
}

// strArrays is every `name[] = {"..", ...}` string table.
func strArrays(src string) map[string][]string {
	out := map[string][]string{}
	for _, m := range regexp.MustCompile(`(?s)(\w+)\[\w*\] = \{\s*(".*?)\};`).FindAllStringSubmatch(src, -1) {
		for _, q := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[2], -1) {
			out[m[1]] = append(out[m[1]], q[1])
		}
	}
	return out
}

// numArray is the numbers of `name[] = {...};`, C octal literals read as octal.
func numArray(t *testing.T, src, name string) []int {
	var out []int
	for _, f := range fields(cut(t, src, `(?s)\b`+name+`\[\w*\] = \{(.*?)\};`)) {
		f = strings.TrimSuffix(strings.TrimPrefix(f, "U("), ")")
		v, err := strconv.ParseInt(f, 0, 32)
		if err != nil {
			t.Fatalf("%s: %q", name, f)
		}
		out = append(out, int(v))
	}
	return out
}

func TestSettingsTableMatchesTheFirmware(t *testing.T) {
	src, hdr := fwSource(t, "main.c"), fwSource(t, "fw.h")
	enum := fields(cut(t, src, `(?s)enum \{ (M_SQL,.*?M_N) \};`))
	idx := map[string]int{}
	for i, e := range enum {
		idx[e] = i
	}
	strs := strArrays(src)
	names := strs["mnames"]
	if len(names) != idx["M_N"] {
		t.Fatalf("mnames has %d names, the enum %d items", len(names), idx["M_N"])
	}
	cfg := cfgFields(t, src)
	consts := map[string]int{}
	for _, e := range [][]string{fields(cut(t, hdr, `enum \{ (PWR_ULP[^}]*)\}`)), fields(cut(t, src, `enum \{ (D_ARC[^}]*)\}`))} {
		for i, n := range e {
			consts[n] = i
		}
	}
	consts["NTHEMES"], _ = strconv.Atoi(cut(t, src, `#define NTHEMES (\d+)`))
	consts["NSTEP"] = len(strs["step_s"])
	val := func(s string) int {
		if m := regexp.MustCompile(`^O\((\w+)\)$`).FindStringSubmatch(s); m != nil {
			f, ok := cfg[m[1]]
			if !ok {
				t.Fatalf("cfg_t has no %s", m[1])
			}
			return f[0]
		}
		if v, ok := consts[s]; ok {
			return v
		}
		v, err := strconv.ParseInt(s, 0, 32)
		if err != nil {
			t.Fatalf("items[]: can't read %q", s)
		}
		return int(v)
	}

	// Every menu item is in the table or is one of these, which hold no value of their own.
	noValue := []string{"M_DTMF", "M_GLINK", "M_GRP", "M_SCOPE", "M_FF", "M_FM", "M_WXM", "M_LH", "M_NAME", "M_TOVFO", "M_DEL", "M_PC", "M_VER"}
	byNum := map[int]setting{}
	for _, s := range settingsTable {
		byNum[s.num] = s
	}
	for i, e := range enum[:len(enum)-1] {
		s, in := byNum[i+1]
		switch {
		case in && slices.Contains(noValue, e):
			t.Errorf("menu %d %s holds no value, but the table has it", i+1, names[i])
		case !in && !slices.Contains(noValue, e):
			t.Errorf("menu %d %s is missing from the settings table", i+1, names[i])
		case in && s.name != names[i]:
			t.Errorf("menu %d is %q in the firmware, %q in the table", i+1, names[i], s.name)
		}
	}

	// items[]: the settings that pick one of n values.
	items := map[int][]string{}
	for _, m := range regexp.MustCompile(`\[(M_\w+)\] = \{([^}]*)\}`).FindAllStringSubmatch(cut(t, src, `(?s)items\[M_N\] = \{(.*?)\n\};`), -1) {
		items[idx[m[1]]+1] = fields(m[2])
	}
	for _, s := range settingsTable {
		it, ok := items[s.num]
		if !ok {
			continue
		}
		bits := val(it[3])
		got := []int{val(it[0]), bits >> 4, bits & 15, val(it[1]), val(it[2])}
		if want := []int{s.off, s.shift, s.width, s.n, s.def}; !slices.Equal(got, want) {
			t.Errorf("%s: firmware byte, bit, width, values, default %v; table %v", s.name, got, want)
		}
		if lab := it[4]; lab == "0" {
			if s.labels != nil {
				t.Errorf("%s: the firmware shows it as a number", s.name)
			}
		} else if !slices.Equal(strs[lab], s.labels) {
			t.Errorf("%s: labels %q in the firmware, %q in the table", s.name, strs[lab], s.labels)
		}
		delete(items, s.num)
	}
	for n := range items {
		t.Errorf("menu %d %s is in items[] but not in the table as one", n, names[n-1])
	}
	for _, s := range settingsTable { // the rest by their cfg field
		if s.field == "" {
			continue
		}
		f, ok := cfg[s.field]
		if !ok {
			t.Errorf("%s: cfg_t has no %s", s.name, s.field)
		} else if f[0] != s.off {
			t.Errorf("%s: cfg.%s is byte %d, the table says %d", s.name, s.field, f[0], s.off)
		}
	}
	for name, at := range map[string]int{"act": cfgAct, "fm": cfgFM, "s": cfgS} {
		if cfg[name][0] != at {
			t.Errorf("cfg.%s is byte %d, the table says %d", name, cfg[name][0], at)
		}
	}
	if !slices.Equal(numArray(t, src, "tones"), ctcssTones) || !slices.Equal(numArray(t, src, "dcs_lo"), dcsCodes) {
		t.Error("the tone menu's CTCSS or DCS list differs from the firmware's")
	}
	var wx []int
	for _, f := range wxFreqs {
		wx = append(wx, int(f))
	}
	if !slices.Equal(numArray(t, src, "wx_f"), wx) {
		t.Error("the weather channels differ from the firmware's")
	}
	if a := cut(t, src, `#define CALL_ADDR (0x[0-9A-F]+)`); a != "0x1E00" || callAddr != 0x1E00 {
		t.Errorf("CALL_ADDR is %s", a)
	}
	if !strings.Contains(src, "ee_read(0x1830 + (uint8_t)(n << 4), CODES + j, 7)") || dtmfAddr != 0x1830 || dtmfLen != 7 {
		t.Error("the DTMF codes are no longer 7 bytes at 0x1830 + 16 (n - 1)")
	}
}

func TestSettingValuesReadAsTheMenuShowsThem(t *testing.T) {
	mem := blank()
	c := mem[cfgAddr:]
	copy(c, make([]byte, 48))
	c[0] = 0xAD
	c[1] = 12             // SQUELCH S3
	c[3] = 3 | 1<<2       // POWER HIGH, SHIFT +
	c[4] = 1<<2 | 1<<4    // DUAL WATCH, KEY BEEP on
	c[8] = 1<<3 | 2<<5    // MODE A AM, B USB
	c[17] = 0b0111        // TX BANDS: three on
	c[15] = 7             // WX ALERT WX7
	c[18] = 0b101         // groups 1 and 3
	c[14] = 11            // PRIORITY channel 11
	c[20] = 0xFB          // FREQ CAL -0.5
	c[22] = 7             // BATT CAL +0.14 V
	c[23], c[24] = 13, 0  // RX TONE 100.0
	c[25] = 51 + 104      // TX TONE D023I
	c[10], c[11] = 0, 120 // OFFSET 0.60, 6.00
	c[12] = 7 << 4        // BRIGHTNESS 7 (0x1FCC bits 4-7)
	copy(mem[callAddr:], "N0CALL\x00\x00")
	want := map[string]string{"SQUELCH": "S3", "POWER": "HIGH", "SHIFT": "+", "DUAL WATCH": "ON", "KEY BEEP": "ON",
		"MODE": "A AM, B USB", "TX BANDS": "H G M", "WX ALERT": "162.550", "SCAN GROUP": "G13", "PRIORITY": "CH011",
		"FREQ CAL": "-0.5 ppm", "BATT CAL": "+0.14 V", "METER CAL": "+0 dB", "RX TONE": "100.0", "TX TONE": "D023I",
		"OFFSET MHz": "0.60 below 300 MHz, 6.00 from 300 MHz", "CALL ID": "N0CALL", "VOX": "OFF", "MIC GAIN": "0",
		"STEP kHz": "2.5", "BRIGHTNESS": "7"}
	for _, s := range settingsTable {
		if w, ok := want[s.name]; ok && s.value(mem) != w {
			t.Errorf("%s = %q, want %q", s.name, s.value(mem), w)
		}
	}
}
