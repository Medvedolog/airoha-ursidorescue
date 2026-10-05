package main

import (
	"strings"
	"testing"
)

func TestParseUBIVolumeID(t *testing.T) {
	out := []byte(`UBI: Volume information dump:
ubi0: vol_id          4
ubi0: reserved_pebs   9
ubi0: alignment       1
ubi0: data_pad        0
ubi0: vol_type        4
ubi0: usable_leb_size 126976
ubi0: used_bytes      326231
ubi0: name            fip
`)
	vols := parseUBIVolumes(out)
	if len(vols) != 1 {
		t.Fatalf("volumes=%d", len(vols))
	}
	v := vols[0]
	if v.ID != 4 || v.Name != "fip" || v.Type != 4 || v.ReservedPEBs != 9 || v.UsableLEB != 126976 {
		t.Fatalf("volume=%+v", v)
	}
}

func TestFreshTotalRescueFIPContract(t *testing.T) {
	good := []ubiVol{{ID: 4, Name: "fip", Type: 4, ReservedPEBs: 9, UsableLEB: 126976}}
	if err := validateFreshFIPVolume(good, 326231); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		vols []ubiVol
		want string
	}{
		{"wrong id", []ubiVol{{ID: 3, Name: "fip", Type: 4, ReservedPEBs: 9, UsableLEB: 126976}}, "id=3"},
		{"dynamic", []ubiVol{{ID: 4, Name: "fip", Type: 3, ReservedPEBs: 9, UsableLEB: 126976}}, "type=3"},
		{"too small", []ubiVol{{ID: 4, Name: "fip", Type: 4, ReservedPEBs: 1, UsableLEB: 126976}}, "capacity"},
		{"missing", nil, "exactly one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFreshFIPVolume(tc.vols, 326231)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestMissingBadBlocks(t *testing.T) {
	before := []uint64{0x20000, 0x80000}
	after := []uint64{0x20000, 0x60000, 0x80000}
	if got := missingBadBlocks(before, after); len(got) != 0 {
		t.Fatalf("new bad blocks are allowed, missing=%v", got)
	}
	after = []uint64{0x80000}
	got := missingBadBlocks(before, after)
	if len(got) != 1 || got[0] != 0x20000 {
		t.Fatalf("missing=%v", got)
	}
}

func TestTotalRescueCandidatesFitCanonicalFIPVolume(t *testing.T) {
	a := &App{root: "."}
	for _, id := range []string{"md", "mf"} {
		p := profiles[id]
		fip, _, err := a.rescueProfileFIP(p)
		if err != nil {
			t.Fatalf("%s rescue FIP: %v", id, err)
		}
		if len(fip) == 0 || uint64(len(fip)) > totalRescueFIPVolumeSize {
			t.Fatalf("%s rescue FIP size=0x%x, max=0x%x", id, len(fip), totalRescueFIPVolumeSize)
		}
		bl2, _, _, err := a.rescueProfileBL2(p)
		if err != nil {
			t.Fatalf("%s rescue BL2: %v", id, err)
		}
		if len(bl2) != bl2Size {
			t.Fatalf("%s rescue BL2 size=0x%x", id, len(bl2))
		}
	}
}
