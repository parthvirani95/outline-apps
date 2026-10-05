package core

import (
	"net"
)

// TCPConnHandler handles TCP connections comming from TUN.
type TCPConnHandler interface {
	// Handle handles the conn for target.
	Handle(conn net.Conn, target *net.TCPAddr) error
}

// UDPConnHandler handles UDP connections comming from TUN.
type UDPConnHandler interface {
	// Connect connects the proxy server. Note that target can be nil.
	Connect(conn UDPConn, target *net.UDPAddr) error

	// ReceiveTo will be called when data arrives from TUN.
	ReceiveTo(conn UDPConn, data []byte, addr *net.UDPAddr) error
}

var tcpConnHandler TCPConnHandler
var udpConnHandler UDPConnHandler

// PATCH (N-1): registration takes lwipMutex because lwIP callbacks read the
// registered value under lwipMutex, and a device restart registers new values
// while goroutines of the previous stack may still be running.
func RegisterTCPConnHandler(h TCPConnHandler) {
	lwipMutex.Lock()
	defer lwipMutex.Unlock()
	tcpConnHandler = h
}

// PATCH (N-1): registration takes lwipMutex because lwIP callbacks read the
// registered value under lwipMutex, and a device restart registers new values
// while goroutines of the previous stack may still be running.
func RegisterUDPConnHandler(h UDPConnHandler) {
	lwipMutex.Lock()
	defer lwipMutex.Unlock()
	udpConnHandler = h
}
