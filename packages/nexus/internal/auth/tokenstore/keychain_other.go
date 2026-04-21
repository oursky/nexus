//go:build !darwin

package tokenstore

// KeychainStore is not available on non-Darwin platforms.
// This stub satisfies the Store interface so probe() in store.go compiles;
// it is never actually called because probe() only constructs it on darwin.
type KeychainStore struct{}

func NewKeychainStore() *KeychainStore         { return &KeychainStore{} }
func (k *KeychainStore) Load() (string, bool, error) { return "", false, nil }
func (k *KeychainStore) Save(_ string) error         { return nil }
func (k *KeychainStore) Delete() error               { return nil }
