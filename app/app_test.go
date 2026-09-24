package app

import (
	"errors"
	"strings"
	"testing"
)

func TestFormFor(t *testing.T) {
	cases := map[Risk]ConfirmForm{
		ReadOnly: FormNone, NonPersistent: FormNone, Manual: FormNone,
		SettingsChange: FormButton,
		UBIMetadata:    FormPhrase, Write: FormPhrase, Erase: FormPhrase, IdentityChange: FormPhrase,
	}
	for r, want := range cases {
		if got := FormFor(r); got != want {
			t.Errorf("FormFor(%s) = %v, want %v", r, got, want)
		}
	}
}

func TestValidateRejectsWeakConfirmation(t *testing.T) {
	for _, r := range []Risk{UBIMetadata, Write, Erase, IdentityChange} {
		if err := (ConfirmRequest{Risk: r}).Validate(); err == nil {
			t.Errorf("%s without a phrase must be rejected", r)
		}
		if err := (ConfirmRequest{Risk: r, Phrase: "WRITE FIP"}).Validate(); err != nil {
			t.Errorf("%s with a phrase: %v", r, err)
		}
	}
	if err := (ConfirmRequest{Risk: "SOMETHING"}).Validate(); err == nil {
		t.Error("unknown risk must be rejected")
	}
	if err := (ConfirmRequest{Risk: Write, Phrase: "ЗАПИСЬ"}).Validate(); err == nil {
		t.Error("non-ASCII phrase must be rejected")
	}
}

func TestCheckAnswer(t *testing.T) {
	c := ConfirmRequest{Risk: Erase, Phrase: "RESTORE STOCK BACKUP"}
	if CheckAnswer(c, "RESTORE STOCK BACKUP") != nil {
		t.Error("exact phrase must confirm")
	}
	for _, a := range []string{"", "restore stock backup", "RESTORE STOCK BACKUP ", "y"} {
		if !errors.Is(CheckAnswer(c, a), ErrCancelled) {
			t.Errorf("answer %q must cancel", a)
		}
	}
	b := ConfirmRequest{Risk: SettingsChange}
	if CheckAnswer(b, "y") != nil || CheckAnswer(b, "да") != nil {
		t.Error("button form accepts y/да")
	}
	if !errors.Is(CheckAnswer(b, ""), ErrCancelled) {
		t.Error("button form: empty answer cancels")
	}
}

func TestRecorder(t *testing.T) {
	r := &Recorder{Answers: []string{"2"}, ConfirmAnswers: []string{"WRITE FIP"}}
	u := r.UI()
	u.Event(Event{Level: LevelInfo, Text: "hello"})
	if v, _ := u.Ask(AskRequest{Prompt: "?"}); v != "2" {
		t.Fatalf("ask = %q", v)
	}
	if v, _ := u.Ask(AskRequest{Prompt: "?"}); v != "" {
		t.Fatalf("empty queue must answer empty, got %q", v)
	}
	if err := u.Confirm(ConfirmRequest{Risk: Write, Phrase: "WRITE FIP"}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := u.Confirm(ConfirmRequest{Risk: Write, Phrase: "WRITE FIP"}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("empty confirm queue must cancel, got %v", err)
	}
	if err := u.Confirm(ConfirmRequest{Risk: Write}); err == nil || errors.Is(err, ErrCancelled) {
		t.Fatalf("invalid request must fail validation, got %v", err)
	}
	u.Output([]byte("uart"))
	if string(r.Output) != "uart" || len(r.Events) != 1 {
		t.Fatal("recorder did not keep output/events")
	}
}

func TestQuickFromPrompt(t *testing.T) {
	for _, tc := range []struct {
		prompt string
		keys   string
		def    string
	}{
		{"Mask MAC addresses? [y/N]: ", "y,n", "n"},
		{"Continue? [Y/n]: ", "y,n", "y"},
		{"Target [bl2/ubi]: ", "bl2,ubi", ""},
		{"Choice [1]: ", "", "1"},
		{"Path to the .fip: ", "", ""},
		{"Enter the local IP after configuring it [192.168.1.254]: ", "", ""},
	} {
		q, def := QuickFromPrompt(tc.prompt)
		var keys []string
		for _, c := range q {
			keys = append(keys, c.Key)
		}
		if strings.Join(keys, ",") != tc.keys || def != tc.def {
			t.Errorf("%q: quick=%v default=%q, want %s / %q", tc.prompt, keys, def, tc.keys, tc.def)
		}
	}
}
