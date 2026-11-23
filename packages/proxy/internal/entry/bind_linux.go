//go:build linux

package entry

import "syscall"

// bindToDevice returns a dialer control that pins a socket to one interface.
func bindToDevice(name string) func(network, address string, raw syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		var bindErr error
		if err := raw.Control(func(fd uintptr) {
			bindErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, name)
		}); err != nil {
			return err
		}
		return bindErr
	}
}
