package main

import "ursidorescue/app"

// Serial is the serial port as the core uses it. Operations get it as a lease
// from the application layer's PortOwner (a.openPort), never by opening the
// device themselves.
type Serial = app.Port
