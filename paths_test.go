package main

import (
	"os"
	"path/filepath"
	"testing"

	"ursidorescue/app"
)

func TestRecentPathsKeepsThreeNewestFirst(t *testing.T) {
	var r recentPaths
	for _, p := range []string{"a", "b", "c", "b", "d"} {
		r.remember("stock-restore", p)
	}
	if got := r.list("stock-restore"); len(got) != 3 || got[0] != "d" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("recent %v", got)
	}
	if len(r.list("fip-repair")) != 0 {
		t.Fatal("recent paths leak between operations")
	}
}

// A path is remembered once it checks out, even if the operation then fails,
// and the next ask offers it by number.
func TestAskPathOffersRecent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "mtd16.bin.gz")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &app.Recorder{Answers: []string{f, "1"}}
	a := &App{ui: rec.UI(), opKind: "itb-boot"}
	if got, err := a.askPath("Path: "); err != nil || got != f {
		t.Fatalf("typed path: %q %v", got, err)
	}
	got, err := a.askPath("Path: ")
	if err != nil || got != f {
		t.Fatalf("recent pick: %q %v", got, err)
	}
	last := rec.Asks[len(rec.Asks)-1]
	if len(last.Choices) == 0 || last.Choices[0].Key != "1" || last.Choices[0].Label != f {
		t.Fatalf("the second ask must offer the recent path: %+v", last.Choices)
	}
	if _, err := a.askPath("Path: "); err == nil {
		t.Fatal("an empty answer must still be an error")
	}
}
