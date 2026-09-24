//go:build !windows

package main

func isExpectedUDPNoise(error) bool { return false }
