package probe

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ExportOptions control the porting bundle.
type ExportOptions struct {
	OutDir string // where the zip goes (default: parent of the probe dir)
	Redact bool   // mask MAC addresses and sensitive env values in text files
	Now    func() time.Time
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "unknown"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// BundleName is ursus-probe-<vendor>-<model>-<soc>-<timestamp>.zip.
func BundleName(p *Profile, now time.Time) string {
	vendor, model, soc := "unknown", "unknown", "unknown"
	if f := p.Device["vendor"]; f != nil && f.Value != nil {
		vendor = fmt.Sprint(f.Value)
	}
	if f := p.Device["compatible"]; f != nil && f.Value != nil {
		if _, m, ok := strings.Cut(fmt.Sprint(f.Value), ","); ok {
			model = m
		}
	} else if f := p.Device["model"]; f != nil && f.Value != nil {
		model = fmt.Sprint(f.Value)
	}
	if p.SoC.Value != nil {
		soc = fmt.Sprint(p.SoC.Value)
	}
	return fmt.Sprintf("ursus-probe-%s-%s-%s-%s.zip", slug(vendor), slug(model), slug(soc), now.Format("20060102-150405"))
}

const readme = `UrsidoRescue porting bundle (%s)
=================================

Created by %s %s in read-only probe mode. No erase, write, saveenv or UBI
change commands were sent; every command is listed in transcript.jsonl.

Start here
  profile.json                    ursus-profile-v1: every value with source + confidence
  ursusboot-porting-report.md     preliminary board-port report for UrsusBoot
  ursusflasher-device-draft.json  DRAFT device profile for UrsusFlasher (not production)
  hashes.sha256                   SHA256 of every file in this bundle

Raw data
  uart.log, uart/                 full UART capture, marker timestamps, port settings
  transcript.jsonl                every command: time, transport, command, response, result
  bootrom/  uboot/  linux/        raw command output per environment
  flash/  mtd/  ubi/              geometry, partition map, raw read-only samples
  dt/                             fdt.dtb / fdt.dts (decompiled without dtc), metadata
  network/  gpio/                 PHY / switch / interfaces, buttons and LEDs

Device-specific data
  uart.log, uboot/env.txt, linux/fw_printenv.txt and network/* may contain
  MAC addresses, serial numbers or GPON identity of THIS unit. profile.json
  lists only where such data lives, never the values. %s

Conflicts (%d) and missing parts are listed in profile.json -> conflicts,
completeness, and at the end of ursusboot-porting-report.md.
`

// sensitiveLineRE matches "key=value" at a line start, or after a JSON-escaped
// newline inside transcript.jsonl.
var sensitiveLineRE = regexp.MustCompile(`(?im)(^|\\n|\\r)((?:eth[0-9]*addr|ethaddr|serial#?|serial_?(?:no|num(?:ber)?)|sn|gpon[a-z_]*|ploam[a-z_]*|[a-z_]*passw(?:or)?d[a-z_]*|loid[a-z_]*)=)([^\\\r\n"]*)`)

func redactText(b []byte) []byte {
	b = macRE.ReplaceAll(b, []byte("xx:xx:xx:xx:xx:xx"))
	return sensitiveLineRE.ReplaceAll(b, []byte("${1}${2}<redacted>"))
}

func isText(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".log", ".json", ".jsonl", ".md", ".dts":
		return true
	}
	return false
}

// Export writes README.txt and hashes.sha256 into dir and zips it.
func Export(dir string, p *Profile, eo ExportOptions) (string, error) {
	if eo.Now == nil {
		eo.Now = time.Now
	}
	if eo.OutDir == "" {
		eo.OutDir = filepath.Dir(filepath.Clean(dir))
	}
	red := "Raw files are included unredacted."
	if eo.Redact {
		red = "This bundle was exported with --redact: MAC addresses and sensitive env values in text files are masked."
	}
	name := BundleName(p, eo.Now())
	rd := fmt.Sprintf(readme, name, p.Generator["tool"], p.Generator["version"], red, len(p.Conflicts))
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte(rd), 0o644); err != nil {
		return "", err
	}
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if rel == "hashes.sha256" || strings.HasSuffix(rel, ".zip") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if eo.Redact && isText(rel) {
			b = redactText(b)
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	sort.Strings(names)
	var hs bytes.Buffer
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&hs, "%s  %s\n", hex.EncodeToString(sum[:]), n)
	}
	if !eo.Redact {
		if err := os.WriteFile(filepath.Join(dir, "hashes.sha256"), hs.Bytes(), 0o644); err != nil {
			return "", err
		}
	}
	files["hashes.sha256"] = hs.Bytes()
	names = append(names, "hashes.sha256")
	if err := os.MkdirAll(eo.OutDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(eo.OutDir, name)
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	root := strings.TrimSuffix(name, ".zip") + "/"
	for _, n := range names {
		h := &zip.FileHeader{Name: root + n, Method: zip.Deflate, Modified: eo.Now()}
		w, err := zw.CreateHeader(h)
		if err != nil {
			f.Close()
			return "", err
		}
		if _, err := w.Write(files[n]); err != nil {
			f.Close()
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return "", err
	}
	return out, f.Close()
}
