//go:build linux

package notices

import "context"

func platformKeychainCredential(context.Context) (string, error) { return "", ErrInvalid }
