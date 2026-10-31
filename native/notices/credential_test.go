//go:build darwin || linux

package notices

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Canonicalize only synthetic fixture directories: macOS temporary roots may
// themselves be exposed through /var -> /private/var. Production never resolves
// credential symlinks and no test opens a real login file.
func nativeCredentialTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("canonical synthetic temp directory unavailable")
	}
	return dir
}
func nativeWriteDummy(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal("write synthetic fixture failed")
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal("set synthetic fixture mode failed")
	}
}

func TestNativeCredentialPath(t *testing.T) {
	home := nativeCredentialTemp(t)
	cases := []struct{ override, want string }{
		{"", filepath.Join(home, ".qqcli", ".env")},
		{"   ", filepath.Join(home, ".qqcli", ".env")},
		{"~/fixture.env", filepath.Join(home, "fixture.env")},
		{filepath.Join(home, "explicit.env"), filepath.Join(home, "explicit.env")},
	}
	for _, tc := range cases {
		got, err := nativeCredentialPath(tc.override, func() (string, error) { return home, nil })
		if err != nil || got != tc.want {
			t.Fatal("credential path selection mismatch")
		}
	}
	if _, err := nativeCredentialPath("~someone/fixture.env", func() (string, error) { return home, nil }); err != ErrInvalid {
		t.Fatal("unexpected shell-like expansion")
	}
	if _, err := nativeCredentialPath("", func() (string, error) { return "", errors.New("private path") }); err != ErrInvalid {
		t.Fatal("home error not sanitized")
	}
	explicit := filepath.Join(home, "explicit.env")
	if got, err := nativeCredentialPath(explicit, func() (string, error) { t.Fatal("explicit absolute path should not query home"); return "", nil }); err != nil || got != explicit {
		t.Fatal("explicit path rejected")
	}
}

func TestNativeDotenvNarrowParsing(t *testing.T) {
	for _, body := range []string{
		"QQ_AI_CONNECT_TOKEN=dummy-token\n",
		"# ignored\nOTHER=value\n QQ_AI_CONNECT_TOKEN = dummy-token \n",
		"export QQ_AI_CONNECT_TOKEN='dummy-token'\n",
		"QQ_AI_CONNECT_TOKEN=\"dummy-token\"\r\n",
	} {
		got, err := parseNativeDotenv([]byte(body))
		if err != nil || got != "dummy-token" {
			t.Fatal("supported dotenv syntax rejected")
		}
	}
	got, err := parseNativeDotenv([]byte(`QQ_AI_CONNECT_TOKEN="dummy\\backslash\"quote"`))
	if err != nil || got != `dummy\backslash"quote` {
		t.Fatal("limited double-quoted unescaping mismatch")
	}
	invalid := []string{
		"", "OTHER=dummy\n", "QQ_AI_CONNECT_TOKEN=\n", "QQ_AI_CONNECT_TOKEN=''", "QQ_AI_CONNECT_TOKEN=\"\"",
		"QQ_AI_CONNECT_TOKEN=a\nQQ_AI_CONNECT_TOKEN=b\n", "QQ_AI_CONNECT_TOKEN=a\nexport QQ_AI_CONNECT_TOKEN=b\n",
		"QQ_AI_CONNECT_TOKEN=\nQQ_AI_CONNECT_TOKEN=b", "QQ_AI_CONNECT_TOKEN=a\nQQ_AI_CONNECT_TOKEN", "QQ_AI_CONNECT_TOKEN a",
		"QQ_AI_CONNECT_TOKEN='unterminated", "QQ_AI_CONNECT_TOKEN=\"unterminated", "QQ_AI_CONNECT_TOKEN='a' trailing",
		`QQ_AI_CONNECT_TOKEN="a\nb"`, `QQ_AI_CONNECT_TOKEN="a\tb"`, `QQ_AI_CONNECT_TOKEN="a\$b"`,
		"QQ_AI_CONNECT_TOKEN=a b", "QQ_AI_CONNECT_TOKEN=a # comment", "QQ_AI_CONNECT_TOKEN='a\tb'",
		"QQ_AI_CONNECT_TOKEN='a\x00b'", "QQ_AI_CONNECT_TOKEN='a\rb'", "QQ_AI_CONNECT_TOKEN=令牌",
		"QQ_AI_CONNECT_TOKEN=a\nOTHER=\x00", "QQ_AI_CONNECT_TOKEN='a'b'", `QQ_AI_CONNECT_TOKEN=a\b`,
		"QQ_AI_CONNECT_TOKEN=" + strings.Repeat("a", maxCredentialFile),
	}
	for i, body := range invalid {
		got, err := parseNativeDotenv([]byte(body))
		if err != ErrInvalid || got != "" {
			t.Errorf("invalid synthetic dotenv %d accepted or exposed in error", i)
		}
	}
}

func TestDefaultCredentialOnlySelectedPrivateDotenv(t *testing.T) {
	home := nativeCredentialTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("QQ_AI_CONNECT_DOTENV", "")
	t.Setenv("QQ_AI_CONNECT_TOKEN", "environment-token-must-not-be-used")
	if err := os.Mkdir(filepath.Join(home, ".qqcli"), 0700); err != nil {
		t.Fatal("fixture setup failed")
	}
	if err := os.Mkdir(filepath.Join(home, ".openclaw"), 0700); err != nil {
		t.Fatal("fixture setup failed")
	}
	legacy := filepath.Join(home, ".openclaw", ".env")
	nativeWriteDummy(t, legacy, "QQ_AI_CONNECT_TOKEN=legacy-must-not-be-used\n", 0600)
	// Injected unavailable Keychain must not search legacy or environment.
	if got, err := defaultCredential(context.Background(), unavailableSyntheticKeychain); err != ErrInvalid || got != "" {
		t.Fatal("missing default did not fail closed")
	}
	selected := filepath.Join(home, ".qqcli", ".env")
	nativeWriteDummy(t, selected, "QQ_AI_CONNECT_TOKEN=default-dummy\n", 0600)
	before, err := os.ReadFile(selected)
	if err != nil {
		t.Fatal("synthetic read failed")
	}
	if got, err := defaultCredential(context.Background(), unavailableSyntheticKeychain); err != nil || got != "default-dummy" {
		t.Fatal("default synthetic credential not resolved")
	}
	after, err := os.ReadFile(selected)
	if err != nil || string(before) != string(after) {
		t.Fatal("credential file was modified")
	}
	if err := os.Chmod(selected, 0644); err != nil {
		t.Fatal("fixture chmod failed")
	}
	if got, err := defaultCredential(context.Background(), unavailableSyntheticKeychain); err != ErrInvalid || got != "" {
		t.Fatal("unsafe existing default fell back")
	}
	explicit := filepath.Join(home, "explicit.env")
	nativeWriteDummy(t, explicit, "QQ_AI_CONNECT_TOKEN=explicit-dummy\n", 0600)
	t.Setenv("QQ_AI_CONNECT_DOTENV", explicit)
	if got, err := defaultCredential(context.Background(), unavailableSyntheticKeychain); err != nil || got != "explicit-dummy" {
		t.Fatal("explicit synthetic selection failed")
	}
	t.Setenv("QQ_AI_CONNECT_DOTENV", filepath.Join(home, "missing.env"))
	if got, err := defaultCredential(context.Background(), unavailableSyntheticKeychain); err != ErrInvalid || got != "" {
		t.Fatal("explicit missing path fell back")
	}
}

func TestNativeCredentialPrivateFileChecks(t *testing.T) {
	dir := nativeCredentialTemp(t)
	path := filepath.Join(dir, "private.env")
	nativeWriteDummy(t, path, "QQ_AI_CONNECT_TOKEN=dummy\n", 0600)
	if got, err := readNativeCredential(context.Background(), path); err != nil || got != "dummy" {
		t.Fatal("safe fixture rejected")
	}
	for _, mode := range []os.FileMode{0000, 0400, 0640, 0644, 0666, 0700, 0600 | os.ModeSetuid, 0600 | os.ModeSetgid} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal("fixture mode setup failed")
		}
		if got, err := readNativeCredential(context.Background(), path); err != ErrInvalid || got != "" {
			t.Errorf("unsafe synthetic mode %v accepted", mode)
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal("fixture reset failed")
	}
	link := filepath.Join(dir, "symlink.env")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal("synthetic symlink failed")
	}
	if _, err := readNativeCredential(context.Background(), link); err != ErrInvalid {
		t.Fatal("credential symlink accepted")
	}
	parent := filepath.Join(dir, "parent-link")
	if err := os.Symlink(dir, parent); err != nil {
		t.Fatal("synthetic parent symlink failed")
	}
	if _, err := readNativeCredential(context.Background(), filepath.Join(parent, "private.env")); err != ErrInvalid {
		t.Fatal("symlink parent accepted")
	}
	hard := filepath.Join(dir, "hardlink.env")
	if err := os.Link(path, hard); err != nil {
		t.Fatal("synthetic hardlink failed")
	}
	if _, err := readNativeCredential(context.Background(), hard); err != ErrInvalid {
		t.Fatal("multiply linked credential accepted")
	}
	if _, err := readNativeCredential(context.Background(), dir); err != ErrInvalid {
		t.Fatal("directory accepted")
	}
	fifo := filepath.Join(dir, "fifo.env")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal("synthetic FIFO creation failed")
	}
	if _, err := readNativeCredential(context.Background(), fifo); err != ErrInvalid {
		t.Fatal("FIFO accepted")
	}
	large := filepath.Join(dir, "large.env")
	nativeWriteDummy(t, large, strings.Repeat("a", maxCredentialFile+1), 0600)
	if _, err := readNativeCredential(context.Background(), large); err != ErrInvalid {
		t.Fatal("oversized file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := readNativeCredential(ctx, path); err != ErrInvalid || got != "" {
		t.Fatal("canceled credential read accepted")
	}
	if got, err := defaultCredential(ctx, unavailableSyntheticKeychain); err != ErrInvalid || got != "" {
		t.Fatal("canceled default resolution accepted")
	}
}

func TestNativeCredentialOwner(t *testing.T) {
	// Exercise the exact descriptor-metadata policy with a mismatched expected
	// owner, without chown, privilege escalation or platform-dependent skipping.
	path := filepath.Join(nativeCredentialTemp(t), "owner.env")
	nativeWriteDummy(t, path, "QQ_AI_CONNECT_TOKEN=dummy\n", 0600)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("synthetic metadata unavailable")
	}
	owner := uint32(os.Geteuid())
	if !safeNativeCredentialFile(info, owner) || safeNativeCredentialFile(info, owner+1) || safeNativeCredentialFile(nil, owner) {
		t.Fatal("credential ownership policy mismatch")
	}
}

func unavailableSyntheticKeychain(context.Context) (string, error) { return "", ErrInvalid }
