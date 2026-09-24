package app

import (
	"errors"
	"sync"
)

// Recorder is a scripted UI for tests and for headless runs: it records
// everything the core reports and answers Ask/Confirm from queues.
type Recorder struct {
	mu        sync.Mutex
	Events    []Event
	Progress  []Progress
	Asks      []AskRequest
	Confirms  []ConfirmRequest
	Artifacts []Artifact
	Output    []byte

	// Answers are returned by Ask in order; an empty queue answers "".
	Answers []string
	// ConfirmAnswers are typed phrases (or "y" for buttons) returned in
	// order; an empty queue cancels.
	ConfirmAnswers []string
}

func (r *Recorder) Event(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, e)
}

func (r *Recorder) ProgressUpdate(p Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Progress = append(r.Progress, p)
}

func (r *Recorder) Ask(q AskRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Asks = append(r.Asks, q)
	if len(r.Answers) == 0 {
		return "", nil
	}
	a := r.Answers[0]
	r.Answers = r.Answers[1:]
	return a, nil
}

func (r *Recorder) Confirm(c ConfirmRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Confirms = append(r.Confirms, c)
	if err := c.Validate(); err != nil {
		return err
	}
	if len(r.ConfirmAnswers) == 0 {
		return ErrCancelled
	}
	a := r.ConfirmAnswers[0]
	r.ConfirmAnswers = r.ConfirmAnswers[1:]
	return CheckAnswer(c, a)
}

func (r *Recorder) WriteOutput(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Output = append(r.Output, b...)
}

func (r *Recorder) AddArtifact(a Artifact) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Artifacts = append(r.Artifacts, a)
}

// UI adapts the recorder to the UI interface.
func (r *Recorder) UI() UI { return recorderUI{r} }

type recorderUI struct{ r *Recorder }

func (u recorderUI) Event(e Event)                    { u.r.Event(e) }
func (u recorderUI) Progress(p Progress)              { u.r.ProgressUpdate(p) }
func (u recorderUI) Ask(q AskRequest) (string, error) { return u.r.Ask(q) }
func (u recorderUI) Confirm(c ConfirmRequest) error   { return u.r.Confirm(c) }
func (u recorderUI) Output(b []byte)                  { u.r.WriteOutput(b) }
func (u recorderUI) Artifact(a Artifact)              { u.r.AddArtifact(a) }

// CheckAnswer decides whether an operator answer confirms c. Front ends use
// it so that every one of them applies the same rule.
func CheckAnswer(c ConfirmRequest, answer string) error {
	if c.Phrase != "" {
		if answer == c.Phrase {
			return nil
		}
		return ErrCancelled
	}
	switch FormFor(c.Risk) {
	case FormNone:
		return nil
	case FormButton:
		if answer == "y" || answer == "yes" || answer == "д" || answer == "да" {
			return nil
		}
		return ErrCancelled
	}
	return errors.New("confirm: phrase form without a phrase")
}
