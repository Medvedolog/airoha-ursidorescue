package app

import "sync/atomic"

// StopFlag carries the operator's STOP from a front end to the core. The
// core polls it only at safe checkpoints; it never aborts a step midway.
type StopFlag struct{ v atomic.Bool }

// Request marks that the operator pressed STOP.
func (f *StopFlag) Request() { f.v.Store(true) }

// Requested reports whether STOP is pending.
func (f *StopFlag) Requested() bool { return f.v.Load() }

// Reset clears the flag at the start of an operation.
func (f *StopFlag) Reset() { f.v.Store(false) }
