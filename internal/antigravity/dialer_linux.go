//go:build linux

package antigravity

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// upstreamDialControl 在 Linux 上为上游 TCP 套接字设置 TCP_USER_TIMEOUT，套接字或内核不支持时忽略
func upstreamDialControl(_, _ string, raw syscall.RawConn) error {
	raw.Control(func(fd uintptr) {
		unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(upstreamTCPUserTimeout.Milliseconds()))
	})
	return nil
}
