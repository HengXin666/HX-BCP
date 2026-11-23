//go:build !linux

package entry

import "syscall"

// bindToDevice is a no-op where interface binding is not supported.
func bindToDevice(string) func(network, address string, raw syscall.RawConn) error {
	return nil
}
