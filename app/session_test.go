package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	now := time.Date(2026, 9, 24, 20, 30, 14, 0, time.UTC)
	if id := NewID("", "probe", now); !regexp.MustCompile(`^20260924-203014-probe-[0-9a-f]{4}$`).MatchString(id) {
		t.Fatalf("session id %q", id)
	}
	if id := NewID("op-", "fip-repair", now); !regexp.MustCompile(`^op-20260924-203014-fip-repair-[0-9a-f]{4}$`).MatchString(id) {
		t.Fatalf("operation id %q", id)
	}
}

func TestSessionFilesAndResult(t *testing.T) {
	work := t.TempDir()
	if _, err := NewSession(work, "Bad Kind", "t", "v", "console"); err == nil {
		t.Fatal("invalid kind must be refused")
	}
	s, err := NewSession(work, "diagnostics", "UrsidoRescue", "0.2.0-test", "console")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(s.Dir) != SessionsDir(work) {
		t.Fatalf("session dir %s not under %s", s.Dir, SessionsDir(work))
	}
	op := s.NewOperation("diagnostics")
	rec := &Recorder{Answers: []string{"secret-password"}, ConfirmAnswers: []string{"WRITE FIP"}}
	u := &SessionUI{Inner: rec.UI(), Session: s, Op: op}
	u.Event(Event{Level: LevelInfo, Text: "U-Boot: mtd list"})
	u.Event(Event{Level: LevelOK, Label: "NET", Text: "PC address"})
	if v, _ := u.Ask(AskRequest{Prompt: "Password: "}); v != "secret-password" {
		t.Fatalf("ask passthrough = %q", v)
	}
	if err := u.Confirm(ConfirmRequest{Risk: Write, Phrase: "WRITE FIP"}); err != nil {
		t.Fatal(err)
	}
	s.Record(map[string]any{"op": op, "event": "command", "command": "mtd list", "rc": 0})
	s.LogError(op, os.ErrNotExist)
	s.Close("success")
	s.Close("failed") // second close is ignored

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(s.Dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(b)
	}
	log := read("session.log")
	if !strings.Contains(log, "U-Boot: mtd list") || !strings.Contains(log, "[NET] PC address") || !strings.Contains(log, "[ASK] Password:") {
		t.Fatalf("session.log misses events:\n%s", log)
	}
	if strings.Contains(log, "secret-password") {
		t.Fatal("an Ask answer (possibly a credential) leaked into session.log")
	}
	ops := read("operations.jsonl")
	if !strings.Contains(ops, `"event":"confirm"`) || !strings.Contains(ops, `"command":"mtd list"`) {
		t.Fatalf("operations.jsonl misses records:\n%s", ops)
	}
	if !strings.Contains(read("errors.log"), op) {
		t.Fatal("errors.log misses the failure")
	}
	var meta sessionMeta
	if err := json.Unmarshal([]byte(read("session.json")), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Result != "success" || meta.Kind != "diagnostics" || len(meta.Operations) != 1 || meta.Operations[0] != op || meta.Finished.IsZero() {
		t.Fatalf("session.json: %+v", meta)
	}
}
