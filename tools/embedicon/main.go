package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

const (
	pe32PlusMagic              = 0x20b
	resourceDirIndex           = 2
	sectionHeaderSize          = 40
	imageScnCntInitializedData = 0x00000040
	imageScnMemRead            = 0x40000000
	rtIcon                     = 3
	rtGroupIcon                = 14
	defaultLang                = 1033
)

var iconSizes = []int{16, 24, 32, 48, 64, 128, 256}

type iconImage struct {
	size int
	data []byte
}

type resLeaf struct {
	data         []byte
	dataEntryOff uint32
	payloadOff   uint32
}

type resEntry struct {
	id   uint32
	dir  *resDir
	leaf *resLeaf
}

type resDir struct {
	entries []resEntry
	off     uint32
}

func align(v, a uint32) uint32 {
	if a == 0 {
		return v
	}
	return (v + a - 1) &^ (a - 1)
}

func rgbaAt(img image.Image, x, y int) color.RGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func resizeBilinear(src image.Image, n int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	sw, sh := b.Dx(), b.Dy()
	for y := 0; y < n; y++ {
		sy := (float64(y)+0.5)*float64(sh)/float64(n) - 0.5
		y0 := int(sy)
		fy := sy - float64(y0)
		if y0 < 0 {
			y0, fy = 0, 0
		}
		if y0 >= sh-1 {
			y0, fy = sh-1, 0
		}
		y1 := y0 + 1
		if y1 >= sh {
			y1 = sh - 1
		}
		for x := 0; x < n; x++ {
			sx := (float64(x)+0.5)*float64(sw)/float64(n) - 0.5
			x0 := int(sx)
			fx := sx - float64(x0)
			if x0 < 0 {
				x0, fx = 0, 0
			}
			if x0 >= sw-1 {
				x0, fx = sw-1, 0
			}
			x1 := x0 + 1
			if x1 >= sw {
				x1 = sw - 1
			}
			c00 := rgbaAt(src, b.Min.X+x0, b.Min.Y+y0)
			c10 := rgbaAt(src, b.Min.X+x1, b.Min.Y+y0)
			c01 := rgbaAt(src, b.Min.X+x0, b.Min.Y+y1)
			c11 := rgbaAt(src, b.Min.X+x1, b.Min.Y+y1)
			mix := func(a, bb, c, d uint8) uint8 {
				v0 := float64(a)*(1-fx) + float64(bb)*fx
				v1 := float64(c)*(1-fx) + float64(d)*fx
				v := v0*(1-fy) + v1*fy
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				return uint8(v + 0.5)
			}
			dst.SetRGBA(x, y, color.RGBA{
				R: mix(c00.R, c10.R, c01.R, c11.R),
				G: mix(c00.G, c10.G, c01.G, c11.G),
				B: mix(c00.B, c10.B, c01.B, c11.B),
				A: mix(c00.A, c10.A, c01.A, c11.A),
			})
		}
	}
	return dst
}

func makeIconImages(pngData []byte) ([]iconImage, error) {
	src, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("decode logo PNG: %w", err)
	}
	if src.Bounds().Dx() != src.Bounds().Dy() {
		return nil, errors.New("logo PNG must be square")
	}
	out := make([]iconImage, 0, len(iconSizes))
	for _, n := range iconSizes {
		var img image.Image = src
		if src.Bounds().Dx() != n {
			img = resizeBilinear(src, n)
		}
		var buf bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, err
		}
		out = append(out, iconImage{size: n, data: buf.Bytes()})
	}
	return out, nil
}

func groupIconData(imgs []iconImage) []byte {
	b := make([]byte, 6+14*len(imgs))
	binary.LittleEndian.PutUint16(b[2:4], 1)
	binary.LittleEndian.PutUint16(b[4:6], uint16(len(imgs)))
	for i, im := range imgs {
		e := b[6+i*14 : 6+(i+1)*14]
		if im.size >= 256 {
			e[0], e[1] = 0, 0
		} else {
			e[0], e[1] = byte(im.size), byte(im.size)
		}
		binary.LittleEndian.PutUint16(e[4:6], 1)
		binary.LittleEndian.PutUint16(e[6:8], 32)
		binary.LittleEndian.PutUint32(e[8:12], uint32(len(im.data)))
		binary.LittleEndian.PutUint16(e[12:14], uint16(i+1))
	}
	return b
}

func makeResourceTree(imgs []iconImage) *resDir {
	iconType := &resDir{}
	for i, im := range imgs {
		lang := &resDir{entries: []resEntry{{id: defaultLang, leaf: &resLeaf{data: im.data}}}}
		iconType.entries = append(iconType.entries, resEntry{id: uint32(i + 1), dir: lang})
	}
	groupLang := &resDir{entries: []resEntry{{id: defaultLang, leaf: &resLeaf{data: groupIconData(imgs)}}}}
	groupType := &resDir{entries: []resEntry{{id: 1, dir: groupLang}}}
	return &resDir{entries: []resEntry{{id: rtIcon, dir: iconType}, {id: rtGroupIcon, dir: groupType}}}
}

func buildResource(root *resDir, baseRVA uint32) []byte {
	var dirs []*resDir
	var leaves []*resLeaf
	var walk func(*resDir)
	walk = func(d *resDir) {
		dirs = append(dirs, d)
		for _, e := range d.entries {
			if e.dir != nil {
				walk(e.dir)
			} else {
				leaves = append(leaves, e.leaf)
			}
		}
	}
	walk(root)
	var off uint32
	for _, d := range dirs {
		d.off = off
		off = align(off+uint32(16+8*len(d.entries)), 4)
	}
	for _, l := range leaves {
		l.dataEntryOff = off
		off += 16
	}
	off = align(off, 4)
	for _, l := range leaves {
		l.payloadOff = off
		off = align(off+uint32(len(l.data)), 4)
	}
	b := make([]byte, off)
	for _, d := range dirs {
		p := d.off
		binary.LittleEndian.PutUint16(b[p+14:p+16], uint16(len(d.entries)))
		ep := p + 16
		for _, e := range d.entries {
			binary.LittleEndian.PutUint32(b[ep:ep+4], e.id)
			if e.dir != nil {
				binary.LittleEndian.PutUint32(b[ep+4:ep+8], e.dir.off|0x80000000)
			} else {
				binary.LittleEndian.PutUint32(b[ep+4:ep+8], e.leaf.dataEntryOff)
			}
			ep += 8
		}
	}
	for _, l := range leaves {
		p := l.dataEntryOff
		binary.LittleEndian.PutUint32(b[p:p+4], baseRVA+l.payloadOff)
		binary.LittleEndian.PutUint32(b[p+4:p+8], uint32(len(l.data)))
		copy(b[l.payloadOff:], l.data)
	}
	return b
}

func inject(exe, logoPNG []byte) ([]byte, error) {
	imgs, err := makeIconImages(logoPNG)
	if err != nil {
		return nil, err
	}
	if len(exe) < 0x40 {
		return nil, errors.New("short EXE")
	}
	pe := int(binary.LittleEndian.Uint32(exe[0x3c:0x40]))
	if pe < 0 || pe+24 > len(exe) || string(exe[pe:pe+4]) != "PE\x00\x00" {
		return nil, errors.New("not PE")
	}
	coff := pe + 4
	nsec := int(binary.LittleEndian.Uint16(exe[coff+2 : coff+4]))
	optSize := int(binary.LittleEndian.Uint16(exe[coff+16 : coff+18]))
	opt := coff + 20
	if opt+optSize > len(exe) || optSize < 136 || binary.LittleEndian.Uint16(exe[opt:opt+2]) != pe32PlusMagic {
		return nil, errors.New("need PE32+ amd64 EXE")
	}
	sectionAlign := binary.LittleEndian.Uint32(exe[opt+32 : opt+36])
	fileAlign := binary.LittleEndian.Uint32(exe[opt+36 : opt+40])
	if sectionAlign == 0 || fileAlign == 0 {
		return nil, errors.New("invalid PE alignments")
	}
	rsrcDD := opt + 112 + resourceDirIndex*8
	if binary.LittleEndian.Uint32(exe[rsrcDD:rsrcDD+4]) != 0 {
		return nil, errors.New("EXE already has a resource directory")
	}
	st := opt + optSize
	firstRaw := uint32(len(exe))
	var maxVAEnd uint32
	for i := 0; i < nsec; i++ {
		sh := st + i*sectionHeaderSize
		vsize := binary.LittleEndian.Uint32(exe[sh+8 : sh+12])
		va := binary.LittleEndian.Uint32(exe[sh+12 : sh+16])
		rawSize := binary.LittleEndian.Uint32(exe[sh+16 : sh+20])
		rawPtr := binary.LittleEndian.Uint32(exe[sh+20 : sh+24])
		if rawPtr != 0 && rawPtr < firstRaw {
			firstRaw = rawPtr
		}
		end := va + vsize
		if rawSize > vsize {
			end = va + rawSize
		}
		if end > maxVAEnd {
			maxVAEnd = end
		}
	}
	newHdr := st + nsec*sectionHeaderSize
	if uint32(newHdr+sectionHeaderSize) > firstRaw {
		return nil, errors.New("no room for another PE section header")
	}
	va := align(maxVAEnd, sectionAlign)
	rsrc := buildResource(makeResourceTree(imgs), va)
	rawPtr := align(uint32(len(exe)), fileAlign)
	rawSize := align(uint32(len(rsrc)), fileAlign)
	out := make([]byte, rawPtr+rawSize)
	copy(out, exe)
	sh := newHdr
	copy(out[sh:sh+8], []byte(".rsrc\x00\x00\x00"))
	binary.LittleEndian.PutUint32(out[sh+8:sh+12], uint32(len(rsrc)))
	binary.LittleEndian.PutUint32(out[sh+12:sh+16], va)
	binary.LittleEndian.PutUint32(out[sh+16:sh+20], rawSize)
	binary.LittleEndian.PutUint32(out[sh+20:sh+24], rawPtr)
	binary.LittleEndian.PutUint32(out[sh+36:sh+40], imageScnCntInitializedData|imageScnMemRead)
	copy(out[rawPtr:], rsrc)
	binary.LittleEndian.PutUint16(out[coff+2:coff+4], uint16(nsec+1))
	binary.LittleEndian.PutUint32(out[opt+8:opt+12], binary.LittleEndian.Uint32(out[opt+8:opt+12])+rawSize)
	binary.LittleEndian.PutUint32(out[opt+56:opt+60], align(va+uint32(len(rsrc)), sectionAlign))
	binary.LittleEndian.PutUint32(out[rsrcDD:rsrcDD+4], va)
	binary.LittleEndian.PutUint32(out[rsrcDD+4:rsrcDD+8], uint32(len(rsrc)))
	return out, nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: embedicon <exe> <logo.png>")
		os.Exit(2)
	}
	exe, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	logo, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	out, err := inject(exe, logo)
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile(os.Args[1], out, 0755); err != nil {
		panic(err)
	}
}
