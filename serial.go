package main

import (
	"errors"
	"fmt"

	"ursidorescue/app"
)

// Serial is the serial port as the core uses it. Operations get it as a lease
// from the application layer's PortOwner (a.openPort), never by opening the
// device themselves.
type Serial = app.Port

// errPortInUse means another program holds the serial port. It is not a
// failure of the operation: the operator closes that program and retries.
var errPortInUse = errors.New("serial port in use by another program")

// portInUseError names the busy port; errors.Is(err, errPortInUse) holds.
type portInUseError struct{ name string }

func (e portInUseError) Error() string {
	return fmt.Sprintf(L("%s занят другой программой (PuTTY, Arduino IDE, другой терминал или второй UrsidoRescue)",
		"%s is held by another program (PuTTY, Arduino IDE, another terminal or a second UrsidoRescue)"), e.name)
}

func (e portInUseError) Is(target error) bool { return target == errPortInUse }

func portInUse(name string) error { return portInUseError{name} }
