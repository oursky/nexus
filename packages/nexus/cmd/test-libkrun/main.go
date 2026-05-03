package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/oursky/nexus/packages/nexus/internal/vm/libkrun"
)

func main() {
	libDir := "/Users/newman/.cache/nexus/bundles/964bf55cb70c0230/lib/darwin-arm64"
	libkrunPath := filepath.Join(libDir, "libkrun.dylib")
	libkrunfwPath := filepath.Join(libDir, "libkrunfw.dylib")
	
	lib, err := libkrun.Load(libkrunPath, libkrunfwPath)
	if err != nil {
		fmt.Println("load error:", err)
		os.Exit(1)
	}
	defer lib.Close()
	
	_ = lib.SetLogLevel(4)
	
	vmCtx, err := lib.NewContext()
	if err != nil {
		fmt.Println("new context error:", err)
		os.Exit(1)
	}
	defer vmCtx.Free()
	
	if err := vmCtx.SetVMConfig(1, 512); err != nil {
		fmt.Println("set vm config error:", err)
		os.Exit(1)
	}
	
	rootfs := "/Users/newman/.cache/nexus/bundles/964bf55cb70c0230/layers/arm64"
	if err := vmCtx.SetRoot(rootfs); err != nil {
		fmt.Println("set root error:", err)
		os.Exit(1)
	}
	
	if err := vmCtx.SetExec("/bin/sh", []string{"-c", "echo hello"}, []string{"PATH=/bin"}); err != nil {
		fmt.Println("set exec error:", err)
		os.Exit(1)
	}
	
	fmt.Println("Starting VM...")
	if err := vmCtx.StartEnter(); err != nil {
		fmt.Println("start enter error:", err)
		os.Exit(1)
	}
	fmt.Println("VM exited successfully")
}
