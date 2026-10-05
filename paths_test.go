package main

import (
	"os"
	"path/filepath"
	"strings"
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

func stubBrowse(t *testing.T, file, dir string) *[]string {
	t.Helper()
	sa, sf, sd := browseAvailable, browseFile, browseDir
	t.Cleanup(func() { browseAvailable, browseFile, browseDir = sa, sf, sd })
	var calls []string
	browseAvailable = func() bool { return true }
	browseFile = func(string, string) (string, error) { calls = append(calls, "file"); return file, nil }
	browseDir = func(string, string) (string, error) { calls = append(calls, "dir"); return dir, nil }
	return &calls
}

// The stock backup may be a folder: its ask offers both dialogs.
func TestBrowseFolderForStock(t *testing.T) {
	calls := stubBrowse(t, "/f/mtd16.bin.gz", "/backups/nokia")
	rec := &app.Recorder{Answers: []string{"**", "*"}}
	a := &App{ui: rec.UI(), opKind: "stock-restore"}
	if got := a.askPathAnswer("Backup: ", true); got != "/backups/nokia" {
		t.Fatalf("folder: %q", got)
	}
	keys := ""
	for _, c := range rec.Asks[0].Choices {
		keys += c.Key + " "
	}
	if keys != "* ** " {
		t.Fatalf("choices %q", keys)
	}
	if got := a.askPathAnswer("Backup: ", true); got != "/f/mtd16.bin.gz" {
		t.Fatalf("file: %q", got)
	}
	if strings.Join(*calls, ",") != "dir,file" {
		t.Fatalf("dialogs %v", *calls)
	}
}

// Elsewhere only a file makes sense: no folder dialog.
func TestBrowseFileOnlyElsewhere(t *testing.T) {
	calls := stubBrowse(t, "/f/x.itb", "/d")
	rec := &app.Recorder{Answers: []string{"**"}}
	a := &App{ui: rec.UI(), opKind: "itb-boot"}
	if got := a.askPathAnswer("ITB: ", false); got != "**" {
		t.Fatalf("got %q", got)
	}
	if len(rec.Asks[0].Choices) != 1 || rec.Asks[0].Choices[0].Key != "*" || len(*calls) != 0 {
		t.Fatalf("choices %+v calls %v", rec.Asks[0].Choices, *calls)
	}
}
