package main

// The Windows exe's icon, version details and manifest, as the resource object (.syso) the Go
// linker takes in. tools/dist.sh makes it for a release:
//
//	TDH3_SYSO=tdh3_windows_amd64.syso TDH3_VERSION=0.67.1 go test -run '^TestWinres$' .
//
// (and TDH3_ICO=tdh3.ico writes the icon on its own too). Without TDH3_SYSO the test builds it in
// memory and checks it. The icon is the flasher's tower (written and still in banner.go), drawn
// from its pixels at each size, so it changes when the tower does.

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

var iconSizes = []int{16, 32, 48, 256}

// iconPicture is the tower finished: the mast and ground green, the tip and two rings amber.
func iconPicture() *lcd {
	l := written(1)
	l.ring(19, pRing)
	l.ring(41, pRing)
	return l
}

var iconColour = map[part]color.NRGBA{
	pDone: {0x00, 0xDC, 0x50, 0xFF},
	pRing: {0xFF, 0xB0, 0x00, 0xFF},
	pTip:  {0xFF, 0xB0, 0x00, 0xFF},
}

// icon draws the tower at s x s pixels on a dark rounded square. The picture's 96 x 81 pixels
// (x 16-111, y 0-80) sit in the middle of a 104-pixel square; each icon pixel averages 8 x 8 samples of it. At
// small sizes each line is first widened, so the 1-pixel mast doesn't fade to nothing.
func icon(s int) *image.NRGBA {
	l := iconPicture()
	grow := map[int]int{16: 2, 32: 1, 48: 1}[s]
	at := func(x, y int) part { // the picture in the square, lines widened by grow pixels
		best := pNone
		for dy := -grow; dy <= grow; dy++ {
			for dx := -grow; dx <= grow; dx++ {
				px, py := x+12+dx, y-11+dy
				if px < 0 || py < 0 || px >= lcdW || py >= lcdH {
					continue
				}
				if p := l[py][px]; iconColour[p].A != 0 && (best == pNone || p == pRing || p == pTip) {
					best = p
				}
			}
		}
		return best
	}
	bg := color.NRGBA{0x1A, 0x1A, 0x1A, 0xFF}
	const sq, k = 104.0, 8
	rad := sq * 0.18
	img := image.NewNRGBA(image.Rect(0, 0, s, s))
	for j := range s {
		for i := range s {
			var r, g, b, a float64
			for sj := range k {
				for si := range k {
					x := (float64(i) + (float64(si)+0.5)/k) * sq / float64(s)
					y := (float64(j) + (float64(sj)+0.5)/k) * sq / float64(s)
					if !inRounded(x, y, sq, rad) {
						continue
					}
					c := bg
					if p := at(int(x), int(y)); p != pNone {
						c = iconColour[p]
					}
					r, g, b, a = r+float64(c.R), g+float64(c.G), b+float64(c.B), a+1
				}
			}
			if a > 0 {
				img.SetNRGBA(i, j, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(a * 255 / (k * k))})
			}
		}
	}
	return img
}

func inRounded(x, y, size, r float64) bool {
	cx, cy := min(max(x, r), size-r), min(max(y, r), size-r)
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
}

// icoFile is the .ico: one PNG for each size, as Windows Vista and later read them.
func icoFile(pngs [][]byte) []byte {
	var b bytes.Buffer
	le := func(v ...any) {
		for _, x := range v {
			binary.Write(&b, binary.LittleEndian, x)
		}
	}
	le(uint16(0), uint16(1), uint16(len(pngs)))
	off := 6 + 16*len(pngs)
	for i, p := range pngs {
		s := uint8(iconSizes[i]) // 256 is written as 0
		le(s, s, uint8(0), uint8(0), uint16(1), uint16(32), uint32(len(p)), uint32(off))
		off += len(p)
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}

// Resource types and the language every resource is in (US English).
const (
	rtIcon, rtGroupIcon, rtVersion, rtManifest = 3, 14, 16, 24
	langEN                                     = 0x0409
)

type resource struct {
	typ, id uint16
	data    []byte
}

// winres is everything tools/dist.sh links into tdh3.exe for version ver ("0.67.1").
func winres(ver string) (syso, ico []byte, err error) {
	var pngs [][]byte
	for _, s := range iconSizes {
		var b bytes.Buffer
		if err := png.Encode(&b, icon(s)); err != nil {
			return nil, nil, err
		}
		pngs = append(pngs, b.Bytes())
	}
	rs := []resource{}
	var group bytes.Buffer // GRPICONDIR: the .ico's header with each image's resource id for its offset
	group.Write(icoFile(pngs)[:6])
	for i, p := range pngs {
		s := uint8(iconSizes[i])
		binary.Write(&group, binary.LittleEndian, struct {
			W, H, Colours, Res uint8
			Planes, Bits       uint16
			Size               uint32
			ID                 uint16
		}{s, s, 0, 0, 1, 32, uint32(len(p)), uint16(i + 1)})
		rs = append(rs, resource{rtIcon, uint16(i + 1), p})
	}
	rs = append(rs, resource{rtGroupIcon, 1, group.Bytes()},
		resource{rtVersion, 1, versionInfo(ver)},
		resource{rtManifest, 1, []byte(manifest)})
	return coffRsrc(rs), icoFile(pngs), nil
}

// manifest: runs as the user who started it (never asks for administrator), on Windows 10 and 11.
const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="Limehawk.tdh3" version="1.0.0.0" processorArchitecture="amd64"/>
  <description>Flasher and programmer for the TD-H3 radio</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
</assembly>
`

// versionInfo is the VS_VERSIONINFO that Properties > Details shows.
func versionInfo(ver string) []byte {
	var n [4]uint16
	for i, f := range strings.SplitN(ver, ".", 3) {
		v, _ := strconv.Atoi(f)
		n[i] = uint16(v)
	}
	ms, ls := uint32(n[0])<<16|uint32(n[1]), uint32(n[2])<<16|uint32(n[3])
	fixed := new(bytes.Buffer)
	binary.Write(fixed, binary.LittleEndian, [13]uint32{
		0xFEEF04BD, 0x00010000, ms, ls, ms, ls,
		0x3F, 0, // flags mask, flags
		0x00040004, 1, 0, // VOS_NT_WINDOWS32, VFT_APP, no subtype
		0, 0, // no file date
	})
	var strs [][]byte
	for _, kv := range [][2]string{
		{"CompanyName", "Limehawk"},
		{"FileDescription", "TD-H3 flasher"},
		{"FileVersion", ver},
		{"InternalName", "tdh3"},
		{"LegalCopyright", "Copyright (c) 2026 Limehawk. MIT License."},
		{"OriginalFilename", "tdh3.exe"},
		{"ProductName", "td-h3-fw"},
		{"ProductVersion", ver},
	} {
		strs = append(strs, verNode(kv[0], utf16z(kv[1]), len(kv[1])+1, 1))
	}
	table := verNode("040904B0", nil, 0, 1, strs...)
	trans := verNode("Translation", []byte{0x09, 0x04, 0xB0, 0x04}, 4, 0)
	return verNode("VS_VERSION_INFO", fixed.Bytes(), fixed.Len(), 0,
		verNode("StringFileInfo", nil, 0, 1, table),
		verNode("VarFileInfo", nil, 0, 1, trans))
}

// verNode is one block of a VS_VERSIONINFO: length, value length (in characters for text),
// type (1 text, 0 binary), the NUL-ended UTF-16 key, the value and the children, each on 4 bytes.
func verNode(key string, value []byte, valueLen int, typ uint16, children ...[]byte) []byte {
	b := make([]byte, 6, 64)
	b = append(b, utf16z(key)...)
	pad := func() {
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	pad()
	b = append(b, value...)
	for _, c := range children {
		pad()
		b = append(b, c...)
	}
	binary.LittleEndian.PutUint16(b[0:], uint16(len(b)))
	binary.LittleEndian.PutUint16(b[2:], uint16(valueLen))
	binary.LittleEndian.PutUint16(b[4:], typ)
	return b
}

func utf16z(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s + "\x00")) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}

// coffRsrc is an AMD64 COFF object with one .rsrc section: the resource tree (type, id, language),
// its data entries and the data. Each data entry's address is relocated (ADDR32NB) against the
// section, so the linker sets it to where the section lands in the exe.
func coffRsrc(rs []resource) []byte {
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].typ < rs[j].typ || rs[i].typ == rs[j].typ && rs[i].id < rs[j].id
	})
	var types []uint16
	byType := map[uint16][]int{}
	for i, r := range rs {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], i)
	}
	// Offsets: root, a directory per type, a language directory per resource, the data entries,
	// then the data, each 8-byte aligned.
	dir := func(n int) int { return 16 + 8*n }
	off := dir(len(types))
	typeDir := map[uint16]int{}
	for _, t := range types {
		typeDir[t] = off
		off += dir(len(byType[t]))
	}
	langDir := make([]int, len(rs))
	for i := range rs {
		langDir[i] = off
		off += dir(1)
	}
	entry := make([]int, len(rs))
	for i := range rs {
		entry[i] = off
		off += 16
	}
	data := make([]int, len(rs))
	for i, r := range rs {
		off = (off + 7) &^ 7
		data[i] = off
		off += len(r.data)
	}
	sec := make([]byte, (off+7)&^7)
	le32 := func(at int, v uint32) { binary.LittleEndian.PutUint32(sec[at:], v) }
	header := func(at, ids int) { binary.LittleEndian.PutUint16(sec[at+14:], uint16(ids)) }
	header(0, len(types))
	for k, t := range types {
		le32(16+8*k, uint32(t))
		le32(16+8*k+4, 0x80000000|uint32(typeDir[t]))
		header(typeDir[t], len(byType[t]))
		for m, i := range byType[t] {
			le32(typeDir[t]+16+8*m, uint32(rs[i].id))
			le32(typeDir[t]+16+8*m+4, 0x80000000|uint32(langDir[i]))
		}
	}
	var relocs []uint32
	for i, r := range rs {
		header(langDir[i], 1)
		le32(langDir[i]+16, langEN)
		le32(langDir[i]+20, uint32(entry[i]))
		le32(entry[i], uint32(data[i])) // + the section's address, by the relocation
		le32(entry[i]+4, uint32(len(r.data)))
		relocs = append(relocs, uint32(entry[i]))
		copy(sec[data[i]:], r.data)
	}

	var b bytes.Buffer
	le := func(v ...any) {
		for _, x := range v {
			binary.Write(&b, binary.LittleEndian, x)
		}
	}
	const fileHdr, secHdr = 20, 40
	relocAt := fileHdr + secHdr + len(sec)
	symAt := relocAt + 10*len(relocs)
	le(uint16(pe.IMAGE_FILE_MACHINE_AMD64), uint16(1), uint32(0), uint32(symAt), uint32(1), uint16(0), uint16(0))
	le([8]byte{'.', 'r', 's', 'r', 'c'}, uint32(0), uint32(0), uint32(len(sec)), uint32(fileHdr+secHdr),
		uint32(relocAt), uint32(0), uint16(len(relocs)), uint16(0),
		uint32(pe.IMAGE_SCN_CNT_INITIALIZED_DATA|pe.IMAGE_SCN_MEM_READ))
	b.Write(sec)
	for _, at := range relocs {
		le(at, uint32(0), uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	le([8]byte{'.', 'r', 's', 'r', 'c'}, uint32(0), int16(1), uint16(0), uint8(3), uint8(0)) // the section's symbol
	le(uint32(4))                                                                            // empty string table
	return b.Bytes()
}

func TestWinres(t *testing.T) {
	ver := os.Getenv("TDH3_VERSION")
	if ver == "" {
		ver = "0.0.0"
	}
	syso, ico, err := winres(ver)
	if err != nil {
		t.Fatal(err)
	}
	f, err := pe.NewFile(bytes.NewReader(syso))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Sections) != 1 || f.Sections[0].Name != ".rsrc" || len(f.Sections[0].Relocs) != 7 {
		t.Fatalf("object: %d sections, want one .rsrc with 7 relocations", len(f.Sections))
	}
	for i, s := range iconSizes {
		img, err := png.Decode(bytes.NewReader(ico[binary.LittleEndian.Uint32(ico[6+16*i+12:]):]))
		if err != nil || img.Bounds().Dx() != s {
			t.Fatalf("icon %d: %v", s, err)
		}
	}
	if !bytes.Contains(versionInfo("0.67.1"), utf16z("0.67.1")) {
		t.Fatal("version string missing")
	}
	for _, out := range [][2]string{{"TDH3_SYSO", string(syso)}, {"TDH3_ICO", string(ico)}} {
		if p := os.Getenv(out[0]); p != "" {
			if err := os.WriteFile(p, []byte(out[1]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
