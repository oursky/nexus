//go:build darwin

package macvm

import (
	"io"
	"log"
	"net"
	"os"
	"time"
)

// startConsoleSocketCapture dials sockPath repeatedly and streams bytes into logPath
// until the returned stop function completes (after cancelling the goroutine context).
//
// macOS libkrun exposes guest HVC0 on a Unix stream socket created by libkrun; the guest
// is the connecting side. Dialing reads the console bytes from the server's accepted side.
func startConsoleSocketCapture(sockPath, logPath string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			fi, statErr := os.Stat(sockPath)
			if statErr != nil || fi.Mode()&os.ModeSocket == 0 {
				select {
				case <-stop:
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			conn, err := (&net.Dialer{Timeout: 2 * time.Second}).Dial("unix", sockPath)
			if err != nil {
				select {
				case <-stop:
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			f, ferr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if ferr != nil {
				log.Printf("macvm: serial capture open %s: %v", logPath, ferr)
				_ = conn.Close()
				return
			}
			func() {
				defer func() { _ = conn.Close(); _ = f.Close() }()
				_, _ = io.Copy(f, conn)
			}()
			select {
			case <-stop:
				return
			case <-time.After(80 * time.Millisecond):
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}
