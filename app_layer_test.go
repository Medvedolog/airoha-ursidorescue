package main

import (
	"strings"
	"testing"

	"ursidorescue/app"
)

func TestProfileChoiceThroughUI(t *testing.T) {
	rec := &app.Recorder{Answers: []string{"3", "", "9"}}
	a := &App{ui: rec.UI()}
	p, err := chooseProfileInteractive(a)
	if err != nil || p.ID != "mf" {
		t.Fatalf("answer 3 = %+v, %v; want mf", p, err)
	}
	if p, err = chooseProfileInteractive(a); err != nil || p.ID != "auto" {
		t.Fatalf("empty answer = %+v, %v; want auto", p, err)
	}
	if _, err = chooseProfileInteractive(a); err == nil {
		t.Fatal("answer 9 must be rejected")
	}
	q := rec.Asks[0]
	if q.Kind != app.AskChoice || len(q.Choices) != 3 || q.Choices[2].Key != "3" {
		t.Fatalf("profile question is not a 3-way choice: %+v", q)
	}
}

func TestConfirmThroughUI(t *testing.T) {
	rec := &app.Recorder{ConfirmAnswers: []string{"WRITE FIP", "write fip"}}
	a := &App{ui: rec.UI()}
	if err := a.confirm(app.Write, "WRITE FIP"); err != nil {
		t.Fatalf("exact phrase: %v", err)
	}
	err := a.confirm(app.Write, "WRITE FIP")
	if err == nil || !strings.Contains(err.Error(), L("отменена", "cancelled")) {
		t.Fatalf("wrong phrase must cancel with the operator message, got %v", err)
	}
	if err := a.confirm(app.Erase, ""); err == nil {
		t.Fatal("ERASE without a phrase must be refused")
	}
	if rec.Confirms[0].Risk != app.Write || rec.Confirms[0].Phrase != "WRITE FIP" {
		t.Fatalf("confirm request not passed through: %+v", rec.Confirms[0])
	}
}

func TestPrerequisitesAreOneSection(t *testing.T) {
	rec := &app.Recorder{}
	a := &App{ui: rec.UI()}
	a.showNetworkPrerequisites()
	if len(rec.Events) < 6 {
		t.Fatalf("too few events: %d", len(rec.Events))
	}
	if rec.Events[0].Kind != app.KindSectionStart || rec.Events[len(rec.Events)-1].Kind != app.KindSectionEnd {
		t.Fatal("prerequisites must be framed as one section")
	}
	for _, e := range rec.Events[1 : len(rec.Events)-1] {
		if e.Label == "" {
			t.Fatalf("prerequisite row without label: %+v", e)
		}
	}
}
