package main

import "time"

type Serial interface {
	Read([]byte, time.Duration) (int, error)
	Write([]byte) error
	ResetInput() error
	Close() error
	Name() string
}
