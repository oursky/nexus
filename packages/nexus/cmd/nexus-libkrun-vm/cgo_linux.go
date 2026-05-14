//go:build linux && cgo

package main

/*
// Vendored libkrun.h under include/; link against cmd/nexus/libkrun-embed.so
// (run scripts/ci/stage-nexus-linux-embeds.sh or task build first on linux/amd64).
// Scripts may still set CGO_CFLAGS / CGO_LDFLAGS to override (e.g. smolvm tarball paths).
#cgo CFLAGS: -I${SRCDIR}/include
#cgo LDFLAGS: -L${SRCDIR}/../nexus -Wl,-rpath,$$ORIGIN/../lib -l:libkrun-embed.so
#define _GNU_SOURCE
#include <libkrun.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <dlfcn.h>

// COMPAT_FEATURES: CSUM|GUEST_CSUM|GUEST_TSO4|GUEST_UFO|HOST_TSO4|HOST_UFO
#define COMPAT_FEATURES ((1<<0)|(1<<1)|(1<<7)|(1<<10)|(1<<11)|(1<<14))

// helper: convert Go bool to C bool
static bool go_bool(int v) { return v != 0; }

typedef int32_t (*krun_add_net_unixstream_fn)(
  uint32_t,
  const char*,
  int,
  uint8_t *const,
  uint32_t,
  uint32_t
);
typedef int32_t (*krun_add_disk2_fn)(
  uint32_t,
  const char*,
  const char*,
  uint32_t,
  bool
);
typedef int32_t (*krun_add_virtiofs_fn)(
  uint32_t,
  const char*,
  const char*
);
typedef int32_t (*krun_add_virtiofs2_fn)(
  uint32_t,
  const char*,
  const char*,
  uint64_t
);
typedef int32_t (*krun_add_virtiofs3_fn)(
  uint32_t,
  const char*,
  const char*,
  uint64_t,
  bool
);

static int go_krun_has_symbol(const char *sym_name) {
  return dlsym(RTLD_DEFAULT, sym_name) != NULL;
}

static int32_t go_krun_add_net_unixstream(uint32_t ctx, int fd, uint8_t *mac) {
  krun_add_net_unixstream_fn fn =
      (krun_add_net_unixstream_fn)dlsym(RTLD_DEFAULT, "krun_add_net_unixstream");
  if (fn == NULL) {
    // -ENOSYS
    return -38;
  }
  return fn(ctx, NULL, fd, mac, (uint32_t)COMPAT_FEATURES, 0);
}

static int32_t go_krun_add_virtiofs_compat(uint32_t ctx, const char *tag, const char *path, uint64_t shm_size, bool read_only) {
  krun_add_virtiofs3_fn fn3 =
      (krun_add_virtiofs3_fn)dlsym(RTLD_DEFAULT, "krun_add_virtiofs3");
  if (fn3 != NULL) {
    return fn3(ctx, tag, path, shm_size, read_only);
  }
  krun_add_virtiofs_fn fn1 =
      (krun_add_virtiofs_fn)dlsym(RTLD_DEFAULT, "krun_add_virtiofs");
  if (fn1 != NULL) {
    return fn1(ctx, tag, path);
  }
  krun_add_virtiofs2_fn fn2 =
      (krun_add_virtiofs2_fn)dlsym(RTLD_DEFAULT, "krun_add_virtiofs2");
  if (fn2 != NULL) {
    // For older fn2-only builds, libkrun expects a non-zero shm window.
    uint64_t win = shm_size;
    if (win == 0) {
      win = 512ULL * 1024ULL * 1024ULL;
    }
    return fn2(ctx, tag, path, win);
  }
  // -ENOSYS
  return -38;
}

static int32_t go_krun_add_disk_compat(uint32_t ctx, const char *block_id, const char *path, uint32_t format, bool read_only) {
  krun_add_disk2_fn fn2 =
      (krun_add_disk2_fn)dlsym(RTLD_DEFAULT, "krun_add_disk2");
  if (fn2 != NULL) {
    return fn2(ctx, block_id, path, format, read_only);
  }
  if (format == 0) {
    return krun_add_disk(ctx, block_id, path, read_only);
  }
  // -ENOSYS (requested format not supported by legacy krun_add_disk)
  return -38;
}
*/
import "C"
import (
	"fmt"
	"syscall"
	"unsafe"
)

func krunSetLogLevel(level uint32) {
	C.krun_set_log_level(C.uint32_t(level))
}

func krunCreate() (uint32, error) {
	ret := C.krun_create_ctx()
	if ret < 0 {
		return 0, fmt.Errorf("krun_create_ctx: errno %d", -ret)
	}
	return uint32(ret), nil
}

func krunSetVMConfig(ctx uint32, vcpus uint8, ramMiB uint32) error {
	ret := C.krun_set_vm_config(C.uint32_t(ctx), C.uint8_t(vcpus), C.uint32_t(ramMiB))
	if ret != 0 {
		return fmt.Errorf("krun_set_vm_config: %w", syscall.Errno(-ret))
	}
	return nil
}

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

func krunSetWorkdir(ctx uint32, workdir string) error {
	wd := C.CString(workdir)
	defer C.free(unsafe.Pointer(wd))
	ret := C.krun_set_workdir(C.uint32_t(ctx), wd)
	if ret != 0 {
		return fmt.Errorf("krun_set_workdir: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunSetEmbeddedKernelCmdline applies a kernel cmdline without specifying an
// external kernel file. Passing a NULL kernel_path to krun_set_kernel tells
// libkrun to use the kernel embedded in libkrunfw.so (which includes virtiofs,
// among other features not present in the minimal stock CI kernel).
func krunSetEmbeddedKernelCmdline(ctx uint32, cmdline string) error {
	var cl *C.char
	if cmdline != "" {
		cl = C.CString(cmdline)
		defer C.free(unsafe.Pointer(cl))
	}
	// kernel_path=NULL → use libkrunfw embedded kernel.
	ret := C.krun_set_kernel(C.uint32_t(ctx), nil, 0, nil, cl)
	if ret != 0 {
		return fmt.Errorf("krun_set_kernel(embedded): %w", syscall.Errno(-ret))
	}
	return nil
}

func krunAddDisk(ctx uint32, blockID, path string, readOnly bool) error {
	return krunAddDiskWithFormat(ctx, blockID, path, 0, readOnly)
}

// krunAddDiskWithFormat adds a block device with the specified image format.
// format: 0=raw, 1=qcow2, 2=vmdk
func krunAddDiskWithFormat(ctx uint32, blockID, path string, format uint32, readOnly bool) error {
	bid := C.CString(blockID)
	defer C.free(unsafe.Pointer(bid))
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	ret := C.go_krun_add_disk_compat(C.uint32_t(ctx), bid, p, C.uint32_t(format), C.go_bool(C.int(boolToInt(readOnly))))
	if ret != 0 {
		return fmt.Errorf("krun_add_disk %s: %w", blockID, syscall.Errno(-ret))
	}
	return nil
}

func krunSetRootDiskRemount(ctx uint32, device, fstype, options string) error {
	dev := C.CString(device)
	defer C.free(unsafe.Pointer(dev))
	var ft *C.char
	if fstype != "" {
		ft = C.CString(fstype)
		defer C.free(unsafe.Pointer(ft))
	}
	var opts *C.char
	if options != "" {
		opts = C.CString(options)
		defer C.free(unsafe.Pointer(opts))
	}
	ret := C.krun_set_root_disk_remount(C.uint32_t(ctx), dev, ft, opts)
	if ret != 0 {
		return fmt.Errorf("krun_set_root_disk_remount %s: %w", device, syscall.Errno(-ret))
	}
	return nil
}

func krunAddVirtioFS3(ctx uint32, tag, path string, daxWindowBytes uint64, readOnly bool) error {
	t := C.CString(tag)
	defer C.free(unsafe.Pointer(t))
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	ret := C.go_krun_add_virtiofs_compat(
		C.uint32_t(ctx),
		t,
		p,
		C.uint64_t(daxWindowBytes),
		C.go_bool(C.int(boolToInt(readOnly))),
	)
	if ret != 0 {
		return fmt.Errorf("krun_add_virtiofs3 tag=%s path=%s: %w", tag, path, syscall.Errno(-ret))
	}
	return nil
}

func krunHasSymbol(name string) bool {
	sym := C.CString(name)
	defer C.free(unsafe.Pointer(sym))
	return C.go_krun_has_symbol(sym) != 0
}

func krunAddNetUnixStream(ctx uint32, fd int, mac [6]byte) error {
	ret := C.go_krun_add_net_unixstream(
		C.uint32_t(ctx),
		C.int(fd),
		(*C.uint8_t)(unsafe.Pointer(&mac[0])),
	)
	if ret != 0 {
		return fmt.Errorf("krun_add_net_unixstream: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunSetPortMap configures TSI TCP port forwarding.
// portMap is a slice of "hostPort:guestPort" strings; a nil slice tells
// libkrun to auto-expose all listening guest ports.
func krunSetPortMap(ctx uint32, portMap []string) error {
	if len(portMap) == 0 {
		ret := C.krun_set_port_map(C.uint32_t(ctx), nil)
		if ret != 0 {
			return fmt.Errorf("krun_set_port_map(nil): %w", syscall.Errno(-ret))
		}
		return nil
	}
	cStrs := make([]*C.char, len(portMap)+1)
	for i, s := range portMap {
		cStrs[i] = C.CString(s)
		defer C.free(unsafe.Pointer(cStrs[i]))
	}
	cStrs[len(portMap)] = nil // null terminator
	ret := C.krun_set_port_map(C.uint32_t(ctx), &cStrs[0])
	if ret != 0 {
		return fmt.Errorf("krun_set_port_map: %w", syscall.Errno(-ret))
	}
	return nil
}

func krunAddVsockPort2(ctx uint32, port uint32, socketPath string, listen bool) error {
	sp := C.CString(socketPath)
	defer C.free(unsafe.Pointer(sp))
	ret := C.krun_add_vsock_port2(C.uint32_t(ctx), C.uint32_t(port), sp, C.go_bool(C.int(boolToInt(listen))))
	if ret != 0 {
		return fmt.Errorf("krun_add_vsock_port2 port=%d: %w", port, syscall.Errno(-ret))
	}
	return nil
}

func krunDisableImplicitConsole(ctx uint32) error {
	ret := C.krun_disable_implicit_console(C.uint32_t(ctx))
	if ret != 0 {
		return fmt.Errorf("krun_disable_implicit_console: %w", syscall.Errno(-ret))
	}
	return nil
}

func krunDisableImplicitVSock(ctx uint32) error {
	ret := C.krun_disable_implicit_vsock(C.uint32_t(ctx))
	if ret != 0 {
		return fmt.Errorf("krun_disable_implicit_vsock: %w", syscall.Errno(-ret))
	}
	return nil
}

func krunAddVSock(ctx uint32, features uint32) error {
	ret := C.krun_add_vsock(C.uint32_t(ctx), C.uint32_t(features))
	if ret != 0 {
		return fmt.Errorf("krun_add_vsock: %w", syscall.Errno(-ret))
	}
	return nil
}

func krunAddSerialConsoleDefault(ctx uint32, inputFD, outputFD int) error {
	ret := C.krun_add_serial_console_default(C.uint32_t(ctx), C.int(inputFD), C.int(outputFD))
	if ret != 0 {
		return fmt.Errorf("krun_add_serial_console_default: %w", syscall.Errno(-ret))
	}
	return nil
}

func krunSetConsoleOutput(ctx uint32, path string) error {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	ret := C.krun_set_console_output(C.uint32_t(ctx), p)
	if ret != 0 {
		return fmt.Errorf("krun_set_console_output: %w", syscall.Errno(-ret))
	}
	return nil
}

// krunSetExec sets the binary to run inside the VM as the primary process.
// When called without krun_set_kernel, libkrun automatically uses the kernel
// embedded in libkrunfw.so. envp may be nil to inherit the host environment.
func krunSetExec(ctx uint32, execPath string, envp []string) error {
	ep := C.CString(execPath)
	defer C.free(unsafe.Pointer(ep))

	var envpPtr **C.char
	if len(envp) > 0 {
		cEnvp := make([]*C.char, len(envp)+1)
		for i, e := range envp {
			cEnvp[i] = C.CString(e)
			defer C.free(unsafe.Pointer(cEnvp[i]))
		}
		cEnvp[len(envp)] = nil
		envpPtr = &cEnvp[0]
	}
	// argv[0] = execPath, argv[1] = nil terminator.
	argv := [2]*C.char{ep, nil}
	ret := C.krun_set_exec(C.uint32_t(ctx), ep, &argv[0], envpPtr)
	if ret != 0 {
		return fmt.Errorf("krun_set_exec: %w", syscall.Errno(-ret))
	}
	return nil
}

func krunStartEnter(ctx uint32) error {
	ret := C.krun_start_enter(C.uint32_t(ctx))
	return fmt.Errorf("krun_start_enter: %w", syscall.Errno(-ret))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
