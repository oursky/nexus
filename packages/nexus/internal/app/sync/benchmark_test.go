//go:build benchmark

package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	domainsync "github.com/oursky/nexus/packages/nexus/internal/domain/sync"
)

// BenchmarkSyncStartStop measures session creation and teardown latency
func BenchmarkSyncStartStop(b *testing.B) {
	client, cleanup := setupEmbeddedClient(b)
	defer cleanup()

	alpha := b.TempDir()
	beta := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sessionID, err := client.CreateSession(alpha, beta, sync.Bidirectional)
		if err != nil {
			b.Fatal(err)
		}
		err = client.TerminateSession(sessionID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSyncSmallFile measures sync speed for a single small file
func BenchmarkSyncSmallFile(b *testing.B) {
	benchmarkSyncFileSize(b, 1024) // 1KB
}

// BenchmarkSyncMediumFile measures sync speed for a single medium file
func BenchmarkSyncMediumFile(b *testing.B) {
	benchmarkSyncFileSize(b, 1024*1024) // 1MB
}

// BenchmarkSyncLargeFile measures sync speed for a single large file
func BenchmarkSyncLargeFile(b *testing.B) {
	benchmarkSyncFileSize(b, 10*1024*1024) // 10MB
}

func benchmarkSyncFileSize(b *testing.B, size int64) {
	client, cleanup := setupEmbeddedClient(b)
	defer cleanup()

	alpha := b.TempDir()
	beta := b.TempDir()

	sessionID, err := client.CreateSession(alpha, beta, sync.Bidirectional)
	if err != nil {
		b.Fatal(err)
	}
	defer client.TerminateSession(sessionID)

	// Wait for initial sync
	time.Sleep(100 * time.Millisecond)

	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filename := fmt.Sprintf("test-file-%d", i)
		err := os.WriteFile(filepath.Join(alpha, filename), content, 0644)
		if err != nil {
			b.Fatal(err)
		}

		// Wait for sync
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(beta, filename)); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	b.SetBytes(size)
}

// BenchmarkSyncManySmallFiles measures sync speed for many small files
func BenchmarkSyncManySmallFiles(b *testing.B) {
	client, cleanup := setupEmbeddedClient(b)
	defer cleanup()

	alpha := b.TempDir()
	beta := b.TempDir()

	sessionID, err := client.CreateSession(alpha, beta, sync.Bidirectional)
	if err != nil {
		b.Fatal(err)
	}
	defer client.TerminateSession(sessionID)

	time.Sleep(100 * time.Millisecond)

	numFiles := 100
	fileSize := 1024 // 1KB each

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create many files
		for j := 0; j < numFiles; j++ {
			filename := fmt.Sprintf("file-%d-%d", i, j)
			content := make([]byte, fileSize)
			os.WriteFile(filepath.Join(alpha, filename), content, 0644)
		}

		// Wait for all files to sync
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			allSynced := true
			for j := 0; j < numFiles; j++ {
				filename := fmt.Sprintf("file-%d-%d", i, j)
				if _, err := os.Stat(filepath.Join(beta, filename)); err != nil {
					allSynced = false
					break
				}
			}
			if allSynced {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	b.SetBytes(int64(numFiles * fileSize))
}

// BenchmarkSyncDirection compares sync performance by direction
func BenchmarkSyncDirection(b *testing.B) {
	for _, direction := range []sync.SyncDirection{sync.Up, sync.Down, sync.Bidirectional} {
		b.Run(direction.String(), func(b *testing.B) {
			client, cleanup := setupEmbeddedClient(b)
			defer cleanup()

			alpha := b.TempDir()
			beta := b.TempDir()

			sessionID, err := client.CreateSession(alpha, beta, direction)
			if err != nil {
				b.Fatal(err)
			}
			defer client.TerminateSession(sessionID)

			time.Sleep(100 * time.Millisecond)

			size := int64(1024 * 1024) // 1MB
			content := make([]byte, size)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				filename := fmt.Sprintf("test-file-%d", i)
				os.WriteFile(filepath.Join(alpha, filename), content, 0644)

				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if _, err := os.Stat(filepath.Join(beta, filename)); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			b.SetBytes(size)
		})
	}
}

func setupEmbeddedClient(b testing.TB) (*EmbeddedMutagenClient, func()) {
	client := NewEmbeddedMutagenClient()
	return client, func() {
		// Cleanup any remaining sessions
		if sessions, _ := client.ListSessions(); len(sessions) > 0 {
			for _, session := range sessions {
				client.TerminateSession(session.SessionID)
			}
		}
	}
}