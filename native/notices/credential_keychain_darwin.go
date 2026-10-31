//go:build darwin

package notices

import "context"

func platformKeychainCredential(ctx context.Context) (string, error) {
	return readKeychainCredential(ctx, runKeychainCommand)
}
