//go:build darwin || linux

package notices

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

const nativeKeychainTimeout = 3 * time.Second

type keychainCommand func(context.Context, string, []string, io.Writer) error

// A bounded writer deliberately retains no stderr and never forwards stdout.
// Overflow fails the command instead of accumulating an unbounded secret.
type keychainOutput struct{ data []byte }

func (w *keychainOutput) Write(p []byte) (int, error) {
	if len(p) > maxCredentialFile+1-len(w.data) {
		return 0, ErrInvalid
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func runKeychainCommand(ctx context.Context, path string, args []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Run()
}

func readKeychainCredential(ctx context.Context, run keychainCommand) (string, error) {
	if ctx == nil || ctx.Err() != nil {
		return "", ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, nativeKeychainTimeout)
	defer cancel()
	out := &keychainOutput{}
	defer func() { clear(out.data) }()
	// Fixed trusted executable and exact native generic-password identity. No shell,
	// interactive unlock, credential arguments, persistence or inherited output.
	if run(bounded, "/usr/bin/security", []string{"find-generic-password", "-s", "qq-cli", "-a", "token", "-w"}, out) != nil || bounded.Err() != nil {
		return "", ErrInvalid
	}
	token := strings.TrimSuffix(string(out.data), "\n")
	if !validNativeToken(token) {
		return "", ErrInvalid
	}
	return token, nil
}
