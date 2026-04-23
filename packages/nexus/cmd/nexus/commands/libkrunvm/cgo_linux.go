//go:build linux && libkrun

package libkrunvm

/*
#include <libkrun.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>

// Kernel format constants (from libkrun.h)
#define FORMAT_ELF     1
#define FORMAT_BZ2     3
#define FORMAT_GZ      4
#define FORMAT_ZSTD    5

// COMPAT_FEATURES: CSUM|GUEST_CSUM|GUEST_TSO4|GUEST_UFO|HOST_TSO4|HOST_UFO
#define COMPAT_FEATURES ((1<<0)|(1<<1)|(1<<7)|(1<<10)|(1<<11)|(1<<14))

// helper: convert Go bool to C bool
static bool go_bool(int v) { return v != 0; }
*/
import "C"
import (
	"fmt"
	"syscall"
	"unsafe"
)

// krunSetLogLevel sets libkrun internal log level (0=off…4=debug).
func krunSetLogLevel(level uint32) {
	C.krun_set_log_level(C.uint32_t(level))
}

// krunCreate returns a new libkrun context id.
func krunCreate() (uint32, error) {
	ret := C.krun_create_ctx()
	if ret < 0 {
		return 0, fmt.Errorf("krun_create_ctx: errno %d", -ret)
	}
	return uint32(ret), nil
}

// krunSetVMConfig configures vCPU count and RAM.
func krunSetVMConfig(ctx uint32, vcpus uint8, ramMiB uint32) error {
	ret := C.krun_set_vm_config(C.uint32_t(ctx), C.uint8_t(vcpus), C.uint32_t(ramMiB))
	if ret != 0 {
		return fmt.Errorf("krun_set_vm_config: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunSetKernel loads a custom kernel (ELF format by default).
func krunSetKernel(ctx uint32, kernelPath, initramfs, cmdline string, format uint32) error {
	kp := C.CString(kernelPath)
	defer C.free(unsafe.Pointer(kp))

	var ir *C.char
	if initramfs != "" {
		ir = C.CString(initramfs)
		defer C.free(unsafe.Pointer(ir))
	}

	var cl *C.char
	if cmdline != "" {
		cl = C.CString(cmdline)
		defer C.free(unsafe.Pointer(cl))
	}

	ret := C.krun_set_kernel(C.uint32_t(ctx), kp, C.uint32_t(format), ir, cl)
	if ret != 0 {
		return fmt.Errorf("krun_set_kernel: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunAddDisk adds a raw block device.
func krunAddDisk(ctx uint32, blockID, path string, readOnly bool) error {
	bid := C.CString(blockID)
	defer C.free(unsafe.Pointer(bid))
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))

	ret := C.krun_add_disk(C.uint32_t(ctx), bid, p, C.go_bool(C.int(boolToInt(readOnly))))
	if ret != 0 {
		return fmt.Errorf("krun_add_disk %s: %w", blockID, syscall.Errno(-ret))
	}
	return nil
}

// krunSetPasstFD connects the VM networking to a passt socket fd.
// Uses the simpler krun_set_passt_fd API (deprecated but widely supported).
func krunSetPasstFD(ctx uint32, fd int) error {
	ret := C.krun_set_passt_fd(C.uint32_t(ctx), C.int(fd))
	if ret != 0 {
		return fmt.Errorf("krun_set_passt_fd: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunAddNetUnixStream connects a virtio-net device to a pre-opened Unix stream fd.
// fd is the host-side of the passt socket pair.
func krunAddNetUnixStream(ctx uint32, fd int) error {
	ret := C.krun_add_net_unixstream(
		C.uint32_t(ctx),
		nil,           // c_path: NULL means use fd
		C.int(fd),
		nil,           // c_mac: NULL = auto
		C.uint32_t(C.COMPAT_FEATURES),
		0,             // flags
	)
	if ret != 0 {
		return fmt.Errorf("krun_add_net_unixstream: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunAddVsockPort2 maps a vsock port to a host Unix socket.
// listen=true: libkrun creates the socket; host dials to reach guest (guest is server).
// listen=false: libkrun expects the host to listen; proxies guest connections to the socket.
func krunAddVsockPort2(ctx uint32, port uint32, socketPath string, listen bool) error {
	sp := C.CString(socketPath)
	defer C.free(unsafe.Pointer(sp))
	ret := C.krun_add_vsock_port2(C.uint32_t(ctx), C.uint32_t(port), sp, C.go_bool(C.int(boolToInt(listen))))
	if ret != 0 {
		return fmt.Errorf("krun_add_vsock_port2 port=%d: %w", port, syscall.Errno(-ret))
	}
	return nil
}

// krunDisableImplicitConsole suppresses the default console device.
func krunDisableImplicitConsole(ctx uint32) error {
	ret := C.krun_disable_implicit_console(C.uint32_t(ctx))
	if ret != 0 {
		return fmt.Errorf("krun_disable_implicit_console: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunAddSerialConsoleDefault adds a legacy serial console (ttyS0) with explicit I/O fds.
func krunAddSerialConsoleDefault(ctx uint32, inputFD, outputFD int) error {
	ret := C.krun_add_serial_console_default(C.uint32_t(ctx), C.int(inputFD), C.int(outputFD))
	if ret != 0 {
		return fmt.Errorf("krun_add_serial_console_default: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunSetConsoleOutput redirects console output to a file.
func krunSetConsoleOutput(ctx uint32, path string) error {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	ret := C.krun_set_console_output(C.uint32_t(ctx), p)
	if ret != 0 {
		return fmt.Errorf("krun_set_console_output: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunStartEnter starts the VM. Does not return on success.
func krunStartEnter(ctx uint32) error {
	ret := C.krun_start_enter(C.uint32_t(ctx))
	// Only reaches here on failure.
	return fmt.Errorf("krun_start_enter: %w", syscall.Errno(-ret))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
