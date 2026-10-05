package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The Linux file dialog is zenity or kdialog, when a graphical session and
// one of them are there; otherwise the path is typed.

func linuxPicker() string {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return ""
	}
	for _, p := range []string{"zenity", "kdialog"} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

func filePickerAvailable() bool { return linuxPicker() != "" }

// pickFile returns the chosen path, "" when the dialog was cancelled.
func pickFile(title, dir string) (string, error) {
	var cmd *exec.Cmd
	switch linuxPicker() {
	case "zenity":
		args := []string{"--file-selection", "--title=" + title}
		if dir != "" {
			args = append(args, "--filename="+dir+string(filepath.Separator))
		}
		cmd = exec.Command("zenity", args...)
	case "kdialog":
		start := dir
		if start == "" {
			start = "."
		}
		cmd = exec.Command("kdialog", "--title", title, "--getopenfilename", start)
	default:
		return "", errors.New("no zenity or kdialog")
	}
	return runPicker(cmd)
}

// pickDir returns the chosen folder, "" when the dialog was cancelled.
func pickDir(title, dir string) (string, error) {
	var cmd *exec.Cmd
	switch linuxPicker() {
	case "zenity":
		args := []string{"--file-selection", "--directory", "--title=" + title}
		if dir != "" {
			args = append(args, "--filename="+dir+string(filepath.Separator))
		}
		cmd = exec.Command("zenity", args...)
	case "kdialog":
		start := dir
		if start == "" {
			start = "."
		}
		cmd = exec.Command("kdialog", "--title", title, "--getexistingdirectory", start)
	default:
		return "", errors.New("no zenity or kdialog")
	}
	return runPicker(cmd)
}

func runPicker(cmd *exec.Cmd) (string, error) {
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil // cancelled
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
