//go:build !(darwin && dev)

package tokenstore

func profileBackingStore() Store {
	return probe()
}
