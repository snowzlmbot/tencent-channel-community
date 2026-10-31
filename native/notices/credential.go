//go:build darwin || linux

package notices

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxCredentialFile = 64 << 10
const nativeTokenKey = "QQ_AI_CONNECT_TOKEN"

// DefaultCredential prefers the native macOS Keychain item, then the selected
// private dotenv file. Linux keeps dotenv-only behavior. Tokens remain in memory;
// no environment token, MCPorter, legacy migration or persistence is performed.
func DefaultCredential(ctx context.Context) (string, error) {
	return defaultCredential(ctx, platformKeychainCredential)
}

// Injected seam keeps all credential tests independent of the real Keychain.
func defaultCredential(ctx context.Context, keychain CredentialProvider) (string, error) {
	if ctx == nil || ctx.Err() != nil {
		return "", ErrInvalid
	}
	if token, err := keychain(ctx); err == nil && validNativeToken(token) && ctx.Err() == nil {
		return token, nil
	}
	if ctx.Err() != nil {
		return "", ErrInvalid
	}
	path, err := nativeCredentialPath(os.Getenv("QQ_AI_CONNECT_DOTENV"), os.UserHomeDir)
	if err != nil {
		return "", ErrInvalid
	}
	return readNativeCredential(ctx, path)
}

func nativeCredentialPath(override string, home func() (string, error)) (string, error) {
	path := strings.TrimSpace(override)
	if path == "" || path == "~" || strings.HasPrefix(path, "~/") {
		base, err := home()
		if err != nil || base == "" {
			return "", ErrInvalid
		}
		if path == "" {
			path = filepath.Join(base, ".qqcli", ".env")
		} else if path == "~" {
			path = base
		} else {
			path = filepath.Join(base, path[2:])
		}
	} else if strings.HasPrefix(path, "~") {
		// No shell expansion, ~other-user resolution or interpolation.
		return "", ErrInvalid
	}
	if !valid(path) {
		return "", ErrInvalid
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", ErrInvalid
	}
	return path, nil
}

// The directories must be trusted against concurrent replacement. As with the
// state store, parent lstat checks reject existing symlinks, not hostile renames.
// Final-component O_NOFOLLOW + descriptor fstat prevents final symlink/FIFO races.
func readNativeCredential(ctx context.Context, path string) (string, error) {
	if ctx == nil || ctx.Err() != nil || !filepath.IsAbs(path) {
		return "", ErrInvalid
	}
	if checkParents(filepath.Dir(path), false) != nil {
		return "", ErrInvalid
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", ErrInvalid
	}
	file := os.NewFile(uintptr(fd), "native credential")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", ErrInvalid
	}
	if !safeNativeCredentialFile(info, uint32(os.Geteuid())) {
		return "", ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialFile+1))
	if err != nil || len(data) > maxCredentialFile || ctx.Err() != nil {
		return "", ErrInvalid
	}
	token, err := parseNativeDotenv(data)
	if err != nil || ctx.Err() != nil {
		return "", ErrInvalid
	}
	return token, nil
}

func safeNativeCredentialFile(info os.FileInfo, owner uint32) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm() == 0600 &&
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 &&
		stat.Uid == owner && stat.Nlink == 1 && info.Size() >= 0 && info.Size() <= maxCredentialFile
}

// parseNativeDotenv is intentionally NOT a full dotenv/shell parser. It accepts
// blank/comment/unrelated-key lines, optional exact `export `, whitespace around
// '=', and bare or paired single/double quoted values. Double quotes support
// only escaped backslash and double quote. No inline comments, interpolation,
// multiline values, duplicate target assignments or arbitrary escape sequences.
// Unrelated keys are ignored, never returned or evaluated.
func parseNativeDotenv(data []byte) (string, error) {
	if len(data) > maxCredentialFile {
		return "", ErrInvalid
	}
	// NUL is forbidden throughout, including ignored lines.
	for _, b := range data {
		if b == 0 {
			return "", ErrInvalid
		}
	}
	found := false
	var token string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, hasEquals := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if key != nativeTokenKey {
			// Malformed attempts at the credential assignment must not hide a second
			// assignment (nor accidentally become a different accepted syntax).
			if strings.HasPrefix(line, nativeTokenKey) && (len(line) == len(nativeTokenKey) || line[len(nativeTokenKey)] == ' ' || line[len(nativeTokenKey)] == '\t') {
				return "", ErrInvalid
			}
			continue
		}
		if !hasEquals || found {
			return "", ErrInvalid
		}
		found = true
		value = strings.TrimSpace(value)
		if value == "" {
			return "", ErrInvalid
		}
		if value[0] == '\'' || value[0] == '"' {
			quote := value[0]
			if len(value) < 2 || value[len(value)-1] != quote {
				return "", ErrInvalid
			}
			inner := value[1 : len(value)-1]
			var decoded strings.Builder
			for i := 0; i < len(inner); i++ {
				c := inner[i]
				if c == quote {
					return "", ErrInvalid
				}
				if quote == '"' && c == '\\' {
					i++
					if i >= len(inner) || inner[i] != '\\' && inner[i] != '"' {
						return "", ErrInvalid
					}
					c = inner[i]
				}
				decoded.WriteByte(c)
			}
			value = decoded.String()
		} else if strings.ContainsAny(value, "\"'\\") {
			return "", ErrInvalid
		}
		if !validNativeToken(value) {
			return "", ErrInvalid
		}
		token = value
	}
	if !found {
		return "", ErrInvalid
	}
	return token, nil
}
