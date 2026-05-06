package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/vm/runner"
)

func main() {
	bundlePath := os.Args[1]
	cmd := os.Args[2:]

	r := runner.Runner{}

	fmt.Fprintf(os.Stderr, "[test] extracting bundle...\n")
	t0 := time.Now()
	eb, err := r.ExtractBundle(bundlePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[test] extract failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[test] extracted in %v\n", time.Since(t0))

	fmt.Fprintf(os.Stderr, "[test] GOOS=%s cacheDir=%s workspaceDir=%s\n", runtime.GOOS, eb.CacheDir, eb.WorkspaceDir)

	ctx := context.Background()
	fmt.Fprintf(os.Stderr, "[test] calling Run with cmd=%v\n", cmd)
	t1 := time.Now()
	err = r.Run(ctx, eb, cmd)
	fmt.Fprintf(os.Stderr, "[test] Run returned in %v: %v\n", time.Since(t1), err)
}
