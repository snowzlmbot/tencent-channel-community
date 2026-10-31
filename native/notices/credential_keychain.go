//go:build darwin || linux

package notices

import (
	"context"
	"encoding/base64"
	"encoding/hex"
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
	return decodeKeychainCredential(out.data)
}

// decodeKeychainCredential mirrors go-keyring's storage formats, not recursive
// decoding. All input is bounded before allocation; failures reveal no values.
func decodeKeychainCredential(raw []byte) (string, error) {
	if len(raw) > maxCredentialFile+1 {
		return "", ErrInvalid
	}
	stored := strings.TrimSpace(string(raw))
	if stored == "" || len(stored) > maxCredentialFile {
		return "", ErrInvalid
	}
	var decoded []byte
	var err error
	switch {
	case strings.HasPrefix(stored, "go-keyring-base64:"):
		body := strings.TrimPrefix(stored, "go-keyring-base64:")
		// DecodeString otherwise tolerates CR/LF. Require canonical standard base64.
		decoded, err = base64.StdEncoding.Strict().DecodeString(body)
		if err == nil && base64.StdEncoding.EncodeToString(decoded) != body {
			err = ErrInvalid
		}
	case strings.HasPrefix(stored, "go-keyring-encoded:"):
		decoded, err = hex.DecodeString(strings.TrimPrefix(stored, "go-keyring-encoded:"))
	default:
		decoded = []byte(stored)
	}
	defer clear(decoded)
	if err != nil {
		return "", ErrInvalid
	}
	token := string(decoded)
	if !validNativeToken(token) || strings.HasPrefix(token, "go-keyring-") {
		return "", ErrInvalid
	}
	return token, nil
}
