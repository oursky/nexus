//go:build darwin && !dev

package tokenstore

func probe() Store {
	return NewKeychainStore()
}
