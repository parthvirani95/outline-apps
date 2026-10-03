// Copyright 2026 The Outline Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package outline

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"

	"golang.getoutline.org/sdk/transport"
)

// SocketProtector marks a socket so it bypasses a system VPN.
//
// Android's VpnService.protect is the implementation. It must run in the VPN
// service process, and only on the sockets that dial the proxy. Every other
// socket from the host app, including the Flutter UI, stays inside the tunnel.
//
// gomobile exports this interface to Java as outline.SocketProtector.
type SocketProtector interface {
	Protect(fd int32) bool
}

var (
	protectorMu     sync.Mutex
	socketProtector SocketProtector
)

// SetSocketProtector installs the process-wide protector used by new clients.
// A nil protector keeps the default dialers, which is what the Cordova client
// uses together with addDisallowedApplication.
func SetSocketProtector(p SocketProtector) {
	protectorMu.Lock()
	socketProtector = p
	protectorMu.Unlock()
}

func currentProtector() SocketProtector {
	protectorMu.Lock()
	defer protectorMu.Unlock()
	return socketProtector
}

func newDirectDialers() (transport.StreamDialer, transport.PacketDialer) {
	if currentProtector() == nil {
		return &transport.TCPDialer{Dialer: net.Dialer{KeepAlive: -1}}, &transport.UDPDialer{}
	}
	dialer := protectedNetDialer()
	return &transport.TCPDialer{Dialer: dialer}, &transport.UDPDialer{Dialer: dialer}
}

func protectedNetDialer() net.Dialer {
	return net.Dialer{
		KeepAlive: -1,
		Control:   protectControl,
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				dnsDialer := &net.Dialer{Control: protectControl}
				return dnsDialer.DialContext(ctx, network, address)
			},
		},
	}
}

func protectControl(network, address string, c syscall.RawConn) error {
	protector := currentProtector()
	if protector == nil {
		return nil
	}
	var failed bool
	if err := c.Control(func(fd uintptr) {
		if !protector.Protect(int32(fd)) {
			failed = true
		}
	}); err != nil {
		return err
	}
	if failed {
		return errors.New("failed to protect socket from VPN")
	}
	return nil
}
