// Package awg is the backend's only way onto the host: every shell command
// it needs - the amneziawg-tools binaries, ip(8), the iptables
// scripts - is issued from here, through a Runner that tests replace.
package awg

import (
	"errors"
	"os/exec"
	"strings"
)

// Runner executes one shell command line and returns its trimmed stdout.
type Runner interface {
	Run(command string) (string, error)
}

// ExitError is a command that ran and failed, with what it said on stderr:
// a bare "exit status 1" from awg-quick says nothing about which line it
// rejected.
type ExitError struct {
	Err    error
	Stderr string
}

func (e *ExitError) Error() string {
	if e.Stderr == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + ": " + e.Stderr
}

func (e *ExitError) Unwrap() error { return e.Err }

// Shell runs commands through bash, which is what the container has and what
// the process substitution in SyncConf needs.
type Shell struct{}

// Run implements Runner.
func (Shell) Run(command string) (string, error) {
	out, err := exec.Command("bash", "-c", command).Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", &ExitError{Err: err, Stderr: strings.TrimSpace(string(exitErr.Stderr))}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
