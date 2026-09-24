package app

import (
	"errors"
	"fmt"
)

// Risk classifies what an operation can change (doc/UI_SPEC_RU.md §14).
type Risk string

const (
	ReadOnly       Risk = "READ_ONLY"
	NonPersistent  Risk = "NON_PERSISTENT"
	SettingsChange Risk = "SETTINGS_CHANGE"
	UBIMetadata    Risk = "UBI_METADATA"
	Write          Risk = "WRITE"
	Erase          Risk = "ERASE"
	IdentityChange Risk = "IDENTITY_CHANGE"
	Manual         Risk = "MANUAL"
)

// ConfirmForm is how a risk class is confirmed (doc/UI_SPEC_RU.md §13).
type ConfirmForm int

const (
	FormNone ConfirmForm = iota
	FormButton
	FormPhrase
)

// FormFor returns the minimum confirmation form for a risk class.
func FormFor(r Risk) ConfirmForm {
	switch r {
	case UBIMetadata, Write, Erase, IdentityChange:
		return FormPhrase
	case SettingsChange:
		return FormButton
	default:
		return FormNone
	}
}

// Known reports whether r is one of the defined classes.
func Known(r Risk) bool {
	switch r {
	case ReadOnly, NonPersistent, SettingsChange, UBIMetadata, Write, Erase, IdentityChange, Manual:
		return true
	}
	return false
}

// Validate rejects a confirmation that is weaker than its risk class needs.
func (c ConfirmRequest) Validate() error {
	if !Known(c.Risk) {
		return fmt.Errorf("confirm: unknown risk class %q", c.Risk)
	}
	if FormFor(c.Risk) == FormPhrase && c.Phrase == "" {
		return fmt.Errorf("confirm: risk %s needs a typed phrase", c.Risk)
	}
	for _, r := range c.Phrase {
		if r < 0x20 || r > 0x7e {
			return errors.New("confirm: the phrase must be printable ASCII")
		}
	}
	return nil
}
