package httpapi

import "net"

// newListener reserves a loopback ephemeral port for tests exercising the real server lifecycle.
func newListener() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
