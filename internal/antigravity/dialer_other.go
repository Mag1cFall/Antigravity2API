//go:build !linux

package antigravity

import "syscall"

// upstreamDialControl 在非 Linux 平台为空，连接判死由 KeepAliveConfig 负责
var upstreamDialControl func(network, address string, raw syscall.RawConn) error
