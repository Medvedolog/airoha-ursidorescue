package main

import (
	"os"
	"testing"
)

func TestBundledLogoBuildsSevenIconSizes(t *testing.T) {
	b, err := os.ReadFile("../../assets/ursus-bear.png")
	if err != nil {
		t.Fatal(err)
	}
	imgs, err := makeIconImages(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != len(iconSizes) {
		t.Fatalf("got %d sizes, want %d", len(imgs), len(iconSizes))
	}
	for i, im := range imgs {
		if im.size != iconSizes[i] || len(im.data) == 0 {
			t.Fatalf("bad icon image %d: size=%d bytes=%d", i, im.size, len(im.data))
		}
	}
	g := groupIconData(imgs)
	if len(g) != 6+14*len(iconSizes) {
		t.Fatalf("bad RT_GROUP_ICON length: %d", len(g))
	}
}
