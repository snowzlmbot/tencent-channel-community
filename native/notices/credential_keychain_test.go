//go:build darwin || linux

package notices

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKeychainCommandSynthetic(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		fail         bool
		want         string
	}{
		{"valid", "synthetic-keychain\n", false, "synthetic-keychain"},
		{"no-newline", "synthetic-keychain", false, "synthetic-keychain"},
		{"empty", "", false, ""}, {"spaces", "a b\n", false, ""},
		{"multiline", "a\nb\n", false, ""}, {"control", "a\r\n", false, ""},
		{"oversized", strings.Repeat("a", maxCredentialFile+2), false, ""},
		{"command-failure", "synthetic-secret", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readKeychainCredential(context.Background(), func(ctx context.Context, path string, args []string, out io.Writer) error {
				if path != "/usr/bin/security" || !reflect.DeepEqual(args, []string{"find-generic-password", "-s", "qq-cli", "-a", "token", "-w"}) {
					t.Fatal("unexpected command capability")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > nativeKeychainTimeout {
					t.Fatal("unbounded command")
				}
				_, writeErr := io.WriteString(out, tc.output)
				if tc.fail {
					return errors.New("synthetic private error")
				}
				return writeErr
			})
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatal("synthetic result mismatch")
			}
			if err != nil && err != ErrInvalid {
				t.Fatal("unsanitized command error")
			}
		})
	}
}

func TestKeychainCancellationSynthetic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if token, err := readKeychainCredential(ctx, func(context.Context, string, []string, io.Writer) error { called = true; return nil }); token != "" || err != ErrInvalid || called {
		t.Fatal("canceled lookup executed")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if token, err := readKeychainCredential(ctx, func(ctx context.Context, _ string, _ []string, out io.Writer) error {
		<-ctx.Done()
		_, _ = io.WriteString(out, "synthetic-late-secret")
		return nil
	}); token != "" || err != ErrInvalid {
		t.Fatal("late lookup accepted")
	}
}

func TestKeychainPreferredAndFallbackSynthetic(t *testing.T) {
	path := filepath.Join(nativeCredentialTemp(t), "stale.env")
	nativeWriteDummy(t, path, "QQ_AI_CONNECT_TOKEN=synthetic-stale\n", 0600)
	t.Setenv("QQ_AI_CONNECT_DOTENV", path)
	before, _ := os.Stat(path)
	token, err := defaultCredential(context.Background(), func(context.Context) (string, error) { return "synthetic-fresh", nil })
	if token != "synthetic-fresh" || err != nil {
		t.Fatal("Keychain did not take precedence")
	}
	for _, provider := range []CredentialProvider{
		unavailableSyntheticKeychain,
		func(context.Context) (string, error) { return "", nil },
		func(context.Context) (string, error) { return "invalid token", nil },
	} {
		if token, err := defaultCredential(context.Background(), provider); token != "synthetic-stale" || err != nil {
			t.Fatal("private dotenv fallback failed")
		}
	}
	after, _ := os.Stat(path)
	body, _ := os.ReadFile(path)
	if !before.ModTime().Equal(after.ModTime()) || string(body) != "QQ_AI_CONNECT_TOKEN=synthetic-stale\n" {
		t.Fatal("credential persisted or migrated")
	}
	// Parent cancellation must never fall back to an older credential.
	ctx, cancel := context.WithCancel(context.Background())
	if token, err := defaultCredential(ctx, func(context.Context) (string, error) { cancel(); return "", ErrInvalid }); token != "" || err != ErrInvalid {
		t.Fatal("canceled lookup fell back")
	}
	// A good Keychain value does not require a usable dotenv file.
	t.Setenv("QQ_AI_CONNECT_DOTENV", path+".missing")
	if token, err := defaultCredential(context.Background(), func(context.Context) (string, error) { return "synthetic-fresh", nil }); token != "synthetic-fresh" || err != nil {
		t.Fatal("Keychain unexpectedly depends on dotenv")
	}
}
