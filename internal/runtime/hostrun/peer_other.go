//go:build !linux

package hostrun

import (
	"errors"
	"net"
)

// peerUID cannot be read here, so no request is served.
func peerUID(net.Conn) (uint32, error) {
	return 0, errors.New("the peer user cannot be read on this system")
}
