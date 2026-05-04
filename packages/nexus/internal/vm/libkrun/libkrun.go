// Package libkrun provides Go bindings for libkrun, a library for creating
// microVMs using Hypervisor.framework on macOS and KVM on Linux.
//
// The library is loaded dynamically at runtime via dlopen (using purego),
// so libkrun does not need to be installed on the build machine. The caller
// provides the path to the libkrun dylib/so extracted from the bundle.
//
// # Usage
//
//	lib, err := libkrun.Load("/path/to/libkrun.dylib", "/path/to/libkrunfw.dylib")
//	if err != nil { ... }
//	defer lib.Close()
//
//	ctx, err := lib.NewContext()
//	if err != nil { ... }
//	defer ctx.Free()
//
//	ctx.SetVMConfig(1, 512)              // 1 vCPU, 512 MiB RAM
//	ctx.SetRoot("/path/to/rootfs")
//	ctx.AddVirtioFS("workspace", "/workspace/src")
//	ctx.SetExec("/sbin/init", nil, nil)
//	ctx.StartEnter()                     // blocks until VM exits
package libkrun

import (
	"crypto/rand"
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Lib holds the dynamically loaded libkrun function pointers.
// Load via [Load]; close via [Lib.Close].
type Lib struct {
	fwHandle  uintptr
	libHandle uintptr
	fns       libkrunFns
}

// libkrunFns contains all function pointers loaded from libkrun.
// Field names use Go conventions; the `C` struct tag maps to the C symbol name.
type libkrunFns struct {
	SetLogLevel      func(level uint32) int32                                                             `C:"krun_set_log_level"`
	CreateCtx        func() int32                                                                         `C:"krun_create_ctx"`
	FreeCtx          func(ctxID uint32) int32                                                             `C:"krun_free_ctx"`
	SetVMConfig      func(ctxID uint32, numVCPUs uint8, ramMiB uint32) int32                              `C:"krun_set_vm_config"`
	SetRootDisk      func(ctxID uint32, blockDev string, fstype string, flags string) int32               `C:"krun_set_root_disk_remount"`
	SetRoot          func(ctxID uint32, rootPath string) int32                                            `C:"krun_set_root"`
	SetWorkdir       func(ctxID uint32, workdir string) int32                                             `C:"krun_set_workdir"`
	SetExec          func(ctxID uint32, execPath string, argv unsafe.Pointer, envp unsafe.Pointer) int32  `C:"krun_set_exec"`
	AddVirtiofs      func(ctxID uint32, tag string, path string) int32                                    `C:"krun_add_virtiofs"`
	AddVsockPort2    func(ctxID uint32, port uint32, path string, listen bool) int32                      `C:"krun_add_vsock_port2"`
	SetPortMap       func(ctxID uint32, portMap unsafe.Pointer) int32                                     `C:"krun_set_port_map"`
	AddDisk2         func(ctxID uint32, blockID string, path string, diskFmt uint32, readonly bool) int32 `C:"krun_add_disk2"`
	SetConsoleOutput func(ctxID uint32, path string) int32                                                `C:"krun_set_console_output"`
	StartEnter       func(ctxID uint32) int32                                                             `C:"krun_start_enter"`

	// Optional symbols — nil if not present in the loaded library.
	DisableImplicitVsock func(ctxID uint32) int32                                                                                               `C:"krun_disable_implicit_vsock"`
	AddVsock             func(ctxID uint32, features uint32) int32                                                                              `C:"krun_add_vsock"`
	SetGPUOptions2       func(ctxID uint32, flags uint32, ramSize uint64) int32                                                                 `C:"krun_set_gpu_options2"`
	AddNetUnixgram       func(ctxID uint32, cPath string, fd int, cMac unsafe.Pointer, features uint32, flags uint32) int32                     `C:"krun_add_net_unixgram"`
	SetKernel            func(ctxID uint32, kernelPath string, kernelFormat uint32, initramfsPath unsafe.Pointer, cmdline unsafe.Pointer) int32 `C:"krun_set_kernel"`
	SetPasstFd           func(ctxID uint32, fd int32) int32                                                                                     `C:"krun_set_passt_fd"`
}

// Load opens libkrunfw (kernel firmware) and libkrun from the given paths
// and resolves all required function symbols. The caller must call Close
// when done.
//
// libkrunfw must be loaded first with RTLD_GLOBAL so that libkrun can
// resolve it by soname.
func Load(libkrunPath, libkrunfwPath string) (*Lib, error) {
	// Load libkrunfw first with RTLD_GLOBAL.
	fwHandle, err := purego.Dlopen(libkrunfwPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("libkrun: failed to load libkrunfw from %s: %w", libkrunfwPath, err)
	}

	libHandle, err := purego.Dlopen(libkrunPath, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		_ = purego.Dlclose(fwHandle)
		return nil, fmt.Errorf("libkrun: failed to load libkrun from %s: %w", libkrunPath, err)
	}

	lib := &Lib{
		fwHandle:  fwHandle,
		libHandle: libHandle,
	}

	if err := lib.loadSymbols(); err != nil {
		_ = purego.Dlclose(libHandle)
		_ = purego.Dlclose(fwHandle)
		return nil, err
	}

	return lib, nil
}

// loadSymbols iterates the fns struct and registers each function pointer
// via purego.RegisterLibFunc, using the `C` tag as the symbol name.
// Fields whose symbol is missing in the library are left nil (optional).
func (l *Lib) loadSymbols() error {
	rv := reflect.Indirect(reflect.ValueOf(&l.fns))
	rt := rv.Type()

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		sym := field.Tag.Get("C")
		if sym == "" {
			continue
		}

		// Check if the symbol exists — purego.RegisterLibFunc panics if not found
		// for required symbols; we need to distinguish required vs optional.
		// Convention: fields of type func(...) int32 with name starting with
		// a capital letter are required; the optional ones we try and skip on error.
		//
		// We detect optionality by whether the field type is a pointer-to-func (nil-able)
		// vs a plain func (non-nil-able). All our fields are plain funcs, so we
		// use a recover-based approach and treat missing symbols as errors for
		// non-optional fields.
		//
		// For simplicity, we mark optional functions by checking if we can look up
		// the symbol first.
		ptr := rv.Field(i).Addr().Interface()
		optional := strings.HasPrefix(field.Name, "Disable") ||
			strings.HasPrefix(field.Name, "AddVsock") ||
			strings.HasPrefix(field.Name, "AddNet") ||
			strings.HasPrefix(field.Name, "SetGPU") ||
			strings.HasPrefix(field.Name, "SetKernel")

		if optional {
			// Try to register; skip on panic (symbol missing).
			err := tryRegisterLibFunc(ptr, l.libHandle, sym)
			if err != nil {
				// Optional: leave as nil zero value (function pointer).
				continue
			}
		} else {
			purego.RegisterLibFunc(ptr, l.libHandle, sym)
		}
	}

	return nil
}

// tryRegisterLibFunc calls purego.RegisterLibFunc and recovers from panics
// (which purego emits when a symbol is not found).
func tryRegisterLibFunc(fn interface{}, handle uintptr, sym string) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("symbol %s not found: %v", sym, r)
		}
	}()
	purego.RegisterLibFunc(fn, handle, sym)
	return nil
}

// Close unloads the library handles.
func (l *Lib) Close() {
	if l.libHandle != 0 {
		_ = purego.Dlclose(l.libHandle)
		l.libHandle = 0
	}
	// Keep libkrunfw resident; libkrun may hold references to symbols in it.
	// In practice the process is short-lived enough that this is fine.
}

// SetLogLevel sets the libkrun log verbosity (0=none, 1=error, 2=warn, 3=info, 4=debug).
func (l *Lib) SetLogLevel(level uint32) error {
	if ret := l.fns.SetLogLevel(level); ret != 0 {
		return fmt.Errorf("krun_set_log_level: %d", ret)
	}
	return nil
}

// NewContext creates a new VM configuration context.
func (l *Lib) NewContext() (*Context, error) {
	id := l.fns.CreateCtx()
	if id < 0 {
		return nil, fmt.Errorf("krun_create_ctx failed: %d", id)
	}
	return &Context{id: uint32(id), lib: l, pinned: nil}, nil
}

// Context is a libkrun VM configuration context.
// It holds the context ID and a reference to the loaded library.
// Call [Context.StartEnter] to boot the VM (blocks until exit).
// Call [Context.Free] when done.
type Context struct {
	id  uint32
	lib *Lib
	// pinned keeps Go-allocated C string data alive for the lifetime of the context.
	pinned [][]byte
}

// Free releases the context. Safe to call multiple times.
func (c *Context) Free() error {
	if c.id == 0 {
		return nil
	}
	ret := c.lib.fns.FreeCtx(c.id)
	c.id = 0
	c.pinned = nil
	if ret != 0 {
		return fmt.Errorf("krun_free_ctx: %d", ret)
	}
	return nil
}

// SetVMConfig sets the number of vCPUs and RAM in MiB.
func (c *Context) SetVMConfig(numVCPUs uint8, ramMiB uint32) error {
	if ret := c.lib.fns.SetVMConfig(c.id, numVCPUs, ramMiB); ret != 0 {
		return fmt.Errorf("krun_set_vm_config: %d", ret)
	}
	return nil
}

// SetRoot sets the rootfs directory path.
func (c *Context) SetRoot(rootPath string) error {
	if ret := c.lib.fns.SetRoot(c.id, rootPath); ret != 0 {
		return fmt.Errorf("krun_set_root: %d", ret)
	}
	return nil
}

// SetWorkdir sets the working directory inside the VM.
func (c *Context) SetWorkdir(workdir string) error {
	if ret := c.lib.fns.SetWorkdir(c.id, workdir); ret != 0 {
		return fmt.Errorf("krun_set_workdir: %d", ret)
	}
	return nil
}

// SetRootDiskRemount configures the VM to mount a block device as root.
func (c *Context) SetRootDiskRemount(device, fstype, options string) error {
	if ret := c.lib.fns.SetRootDisk(c.id, device, fstype, options); ret != 0 {
		return fmt.Errorf("krun_set_root_disk_remount: %d", ret)
	}
	return nil
}

// AddDisk adds a block device image to the VM.
// format: 0=raw, 1=qcow2, 2=vmdk.
func (c *Context) AddDisk(blockID, path string, format uint32, readOnly bool) error {
	if ret := c.lib.fns.AddDisk2(c.id, blockID, path, format, readOnly); ret != 0 {
		return fmt.Errorf("krun_add_disk2(%s): %d", blockID, ret)
	}
	return nil
}

// SetExec sets the binary to execute inside the VM along with argv and environment.
// Pass nil for args/env to use defaults.
func (c *Context) SetExec(execPath string, args []string, env []string) error {
	ret := c.lib.fns.SetExec(c.id, execPath, c.cStringArray(args), c.cStringArray(env))
	if ret != 0 {
		return fmt.Errorf("krun_set_exec: %d", ret)
	}
	return nil
}

// AddVirtioFS mounts a host directory inside the VM via virtio-fs.
// tag is the mount tag used inside the guest (e.g. "workspace").
// path is the host directory to expose.
func (c *Context) AddVirtioFS(tag, path string) error {
	if ret := c.lib.fns.AddVirtiofs(c.id, tag, path); ret != 0 {
		return fmt.Errorf("krun_add_virtiofs: %d", ret)
	}
	return nil
}

// AddVsockPort maps a vsock port to a host Unix socket path.
// If listen is true the host socket will be bound by libkrun;
// if false it must already be listening and libkrun will connect.
func (c *Context) AddVsockPort(port uint32, socketPath string, listen bool) error {
	if ret := c.lib.fns.AddVsockPort2(c.id, port, socketPath, listen); ret != 0 {
		return fmt.Errorf("krun_add_vsock_port2: %d", ret)
	}
	return nil
}

// SetConsoleOutput redirects the VM console to a Unix socket path.
func (c *Context) SetConsoleOutput(socketPath string) error {
	if ret := c.lib.fns.SetConsoleOutput(c.id, socketPath); ret != 0 {
		return fmt.Errorf("krun_set_console_output: %d", ret)
	}
	return nil
}

// StartEnter boots the VM. This call blocks until the VM exits.
// The return value is the VM exit code; a non-zero value is an error.
func (c *Context) StartEnter() error {
	ret := c.lib.fns.StartEnter(c.id)
	if ret != 0 {
		return fmt.Errorf("krun_start_enter: exit code %d", ret)
	}
	return nil
}

// DisableImplicitVsock disables the implicit vsock device that libkrun creates
// by default. After calling this, AddVsock must be called to configure an
// explicit vsock. Returns an error if the symbol is not available.
func (c *Context) DisableImplicitVsock() error {
	if c.lib.fns.DisableImplicitVsock == nil {
		return fmt.Errorf("krun_disable_implicit_vsock: symbol not available in loaded library")
	}
	if ret := c.lib.fns.DisableImplicitVsock(c.id); ret != 0 {
		return fmt.Errorf("krun_disable_implicit_vsock: %d", ret)
	}
	return nil
}

// AddVsock adds an explicit vsock device with the given feature flags.
// Pass 0 for plain vsock without networking; pass TSI feature flags for
// transparent socket impersonation. Returns an error if the symbol is not
// available in the loaded library.
func (c *Context) AddVsock(features uint32) error {
	if c.lib.fns.AddVsock == nil {
		return fmt.Errorf("krun_add_vsock: symbol not available in loaded library")
	}
	if ret := c.lib.fns.AddVsock(c.id, features); ret != 0 {
		return fmt.Errorf("krun_add_vsock: %d", ret)
	}
	return nil
}

// Net feature flags (from uapi/linux/virtio_net.h)
const (
	NetFeatureCSUM      = 1 << 0
	NetFeatureGuestCSUM = 1 << 1
	NetFeatureGuestTSO4 = 1 << 7
	NetFeatureGuestTSO6 = 1 << 8
	NetFeatureGuestUFO  = 1 << 10
	NetFeatureHostTSO4  = 1 << 11
	NetFeatureHostTSO6  = 1 << 12
	NetFeatureHostUFO   = 1 << 14
)

// CompatNetFeatures matches the COMPAT_NET_FEATURES define in libkrun.h.
const CompatNetFeatures = NetFeatureCSUM | NetFeatureGuestCSUM |
	NetFeatureGuestTSO4 | NetFeatureGuestUFO |
	NetFeatureHostTSO4 | NetFeatureHostUFO

// Kernel format constants for krun_set_kernel.
const (
	KernelFormatRaw      = 0
	KernelFormatElf      = 1
	KernelFormatPeGz     = 2
	KernelFormatImageBz2 = 3
	KernelFormatImageGz  = 4
	KernelFormatImageZst = 5
)

// Net flags for virtio-net backends.
const (
	NetFlagVFKit      = 1 << 0 // Send vfkit magic after establishing connection (required for gvproxy vfkit mode)
	NetFlagDHCPClient = 1 << 1 // Configure the guest interface via DHCP
)

// AddNetUnixgram adds a virtio-net device connected to a Unix datagram socket
// backend such as gvproxy (in vfkit mode) or vmnet-helper.
//
// socketPath is the path to the Unix datagram socket. mac may be nil (a random
// locally-administered MAC is generated) or a 6-byte slice. features is a
// bitmask of NetFeature* constants; pass 0 for defaults or CompatNetFeatures.
// flags is a bitmask of NetFlag* constants; for gvproxy in vfkit mode pass
// NetFlagVFKit.
func (c *Context) AddNetUnixgram(socketPath string, mac []byte, features, flags uint32) error {
	if c.lib.fns.AddNetUnixgram == nil {
		return fmt.Errorf("krun_add_net_unixgram: symbol not available in loaded library")
	}
	macBytes := make([]byte, 6)
	if len(mac) == 6 {
		copy(macBytes, mac)
	} else {
		// Generate a random locally-administered MAC.
		// Read from crypto/rand for uniformity.
		_, _ = rand.Read(macBytes)
		macBytes[0] = (macBytes[0] | 0x02) & 0xfe // locally administered, unicast
	}
	c.pinned = append(c.pinned, macBytes)
	macPtr := unsafe.Pointer(unsafe.SliceData(macBytes))
	if ret := c.lib.fns.AddNetUnixgram(c.id, socketPath, -1, macPtr, features, flags); ret != 0 {
		return fmt.Errorf("krun_add_net_unixgram: %d", ret)
	}
	return nil
}

// SetKernel sets a custom kernel to be loaded in the microVM, overriding the
// kernel bundled in libkrunfw. kernelPath is the absolute path to the kernel
// image on the host filesystem. format is one of the KernelFormat* constants;
// for an uncompressed aarch64 Image use KernelFormatRaw. initramfs and cmdline
// may be empty strings.
func (c *Context) SetKernel(kernelPath string, format uint32, initramfs, cmdline string) error {
	if c.lib.fns.SetKernel == nil {
		return fmt.Errorf("krun_set_kernel: symbol not available in loaded library")
	}
	var initramfsPtr, cmdlinePtr unsafe.Pointer
	if initramfs != "" {
		initramfsPtr = c.cString(initramfs)
	}
	if cmdline != "" {
		cmdlinePtr = c.cString(cmdline)
	}
	if ret := c.lib.fns.SetKernel(c.id, kernelPath, format, initramfsPtr, cmdlinePtr); ret != 0 {
		return fmt.Errorf("krun_set_kernel: %d", ret)
	}
	return nil
}

// SetPasstFd connects a passt socket to the microVM's virtio-net device.
// fd is the file descriptor of a connected Unix socket to a running passt
// process. This is the Linux-native alternative to gvproxy on macOS.
func (c *Context) SetPasstFd(fd int) error {
	if c.lib.fns.SetPasstFd == nil {
		return fmt.Errorf("krun_set_passt_fd: symbol not available in loaded library")
	}
	if ret := c.lib.fns.SetPasstFd(c.id, int32(fd)); ret != 0 {
		return fmt.Errorf("krun_set_passt_fd: %d", ret)
	}
	return nil
}

// SetPortMap configures TSI TCP port forwarding.
// Passing nil asks libkrun to auto-expose guest listeners.
func (c *Context) SetPortMap(portMap []string) error {
	ptr := c.cStringArray(portMap)
	if ptr == nil {
		// Some libkrun builds crash on a literal NULL map pointer; pass an
		// explicit empty, null-terminated list instead.
		empty := []unsafe.Pointer{nil}
		b := unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(empty))), uintptr(len(empty))*unsafe.Sizeof(uintptr(0)))
		c.pinned = append(c.pinned, b)
		ptr = unsafe.Pointer(unsafe.SliceData(empty))
	}
	if ret := c.lib.fns.SetPortMap(c.id, ptr); ret != 0 {
		return fmt.Errorf("krun_set_port_map: %d", ret)
	}
	return nil
}

// cString converts a Go string to a null-terminated byte slice, pinning it
// for the lifetime of the context so the pointer remains valid when passed to C.
func (c *Context) cString(s string) unsafe.Pointer {
	if s == "" {
		return nil
	}
	if !strings.HasSuffix(s, "\x00") {
		s += "\x00"
	}
	b := []byte(s)
	c.pinned = append(c.pinned, b)
	return unsafe.Pointer(unsafe.SliceData(b))
}

// cStringArray converts a []string to a null-pointer-terminated array of C strings,
// pinning all allocations for the context lifetime.
func (c *Context) cStringArray(ss []string) unsafe.Pointer {
	if len(ss) == 0 {
		return nil
	}
	ptrs := make([]unsafe.Pointer, len(ss)+1)
	ptrs[len(ss)] = nil // null terminator
	for i, s := range ss {
		ptrs[i] = c.cString(s)
	}
	// Pin the pointer array itself.
	b := unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(ptrs))), uintptr(len(ptrs))*8)
	c.pinned = append(c.pinned, b)
	return unsafe.Pointer(unsafe.SliceData(ptrs))
}
