// make-test-bundle is a small tool that creates a self-contained test .nxbundle
// with a trivial workspace (one hello.txt file) to verify the self-executing stub works.
//
// Usage: go run ./cmd/make-test-bundle/ -nexus /path/to/nexus -out /tmp/test.nxbundle
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/domain/bundle"
)

func main() {
	out := flag.String("out", "/tmp/test.nxbundle", "output path")
	nexusBin := flag.String("nexus", "", "path to the nexus binary to embed (required)")
	flag.Parse()

	if *nexusBin == "" {
		fmt.Fprintln(os.Stderr, "make-test-bundle: -nexus <path> is required")
		os.Exit(1)
	}

	// Build a tiny workspace archive (workspace.tar.gz) with one test file.
	var wsGz bytes.Buffer
	gw := gzip.NewWriter(&wsGz)
	tw := tar.NewWriter(gw)
	content := []byte("Hello from the nexus bundle!\n")
	_ = tw.WriteHeader(&tar.Header{Name: "hello.txt", Mode: 0644, Size: int64(len(content)), ModTime: time.Now()})
	_, _ = tw.Write(content)
	tw.Close()
	gw.Close()

	// No OCI layers for the test bundle (no VM needed — just verifying self-exec).
	assetsTar, err := bundle.BuildAssetsTar(wsGz.Bytes(), nil)
	if err != nil {
		fatalf("build assets: %v", err)
	}

	compressed, err := bundle.CompressZstd(assetsTar)
	if err != nil {
		fatalf("compress: %v", err)
	}

	// Read the nexus binary to embed.
	nexusBytes, err := os.ReadFile(*nexusBin)
	if err != nil {
		fatalf("read nexus binary %s: %v", *nexusBin, err)
	}

	if err := bundle.WriteNXPackBundleWithBinary(*out, compressed, nexusBytes); err != nil {
		fatalf("write bundle: %v", err)
	}

	fi, _ := os.Stat(*out)
	fmt.Printf("wrote %s (%d bytes)\n", *out, fi.Size())
	fmt.Printf("to verify: PATH=/usr/bin:/bin %s\n", *out)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "make-test-bundle: "+format+"\n", args...)
	os.Exit(1)
}
