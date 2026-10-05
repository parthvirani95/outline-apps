// Copyright 2025 The Outline Authors
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

package tun2socks

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"localhost/client/go/outline"
	"localhost/client/go/outline/internal/redact"
	perrs "localhost/client/go/outline/platerrors"
	"localhost/client/go/outline/vpn"
)

// RemoteDevice is an IO device that connects to a remote Outline server.
// It is also responsible for managing the Outline client session.
//
// This type is exported through gomobile, and wraps a [vpn.RemoteDevice].
type RemoteDevice struct {
	mu     sync.Mutex // protect tun modifications and closed
	tun    io.Closer  // will be set in GoRelayTraffic
	closed bool       // set by Close; no relay goroutine may start afterwards
	rd     *vpn.RemoteDevice
	client *outline.Client

	// relays tracks the goroutines started by GoRelayTraffic and
	// GoRelayTrafficOneWay. Add is only called under mu while !closed.
	relays sync.WaitGroup

	closeOnce sync.Once
	closeErr  *perrs.PlatformError
}

// relayStopTimeout bounds how long Close waits for the relay goroutines.
var relayStopTimeout = 2 * time.Second

// ConnectRemoteDeviceResult represents the result of ConnectRemoteDevice.
//
// We use a struct instead of a tuple to preserve a strongly typed error that gobind recognizes.
type ConnectRemoteDeviceResult struct {
	Device *RemoteDevice
	Error  *perrs.PlatformError
}

func ConnectRemoteDevice(client *outline.Client) (res *ConnectRemoteDeviceResult) {
	if err := client.StartSession(); err != nil {
		return &ConnectRemoteDeviceResult{Error: &perrs.PlatformError{
			Code:    perrs.SetupTrafficHandlerFailed,
			Message: "failed to start backend Client session",
			Cause:   perrs.ToPlatformError(err),
		}}
	}
	defer func() {
		if res.Error != nil {
			if err := client.EndSession(); err != nil {
				slog.Warn("failed to end backend Client session", "err", redact.Err(err))
			}
		}
	}()
	rd, err := vpn.ConnectRemoteDevice(context.Background(), client, client)
	if err != nil {
		return &ConnectRemoteDeviceResult{Error: perrs.ToPlatformError(err)}
	}
	return &ConnectRemoteDeviceResult{Device: &RemoteDevice{
		rd:     rd,
		client: client,
	}}
}

func (d *RemoteDevice) GetHealthStatus() *perrs.PlatformError {
	return perrs.ToPlatformError(d.rd.GetHealthStatus())
}

func (d *RemoteDevice) Write(p []byte) (int, error) {
	return d.rd.Write(p)
}

func (d *RemoteDevice) NotifyNetworkChanged() {
	d.client.NotifyNetworkChanged()
}

// Close closes the TUN device and the remote device, waits (bounded by
// relayStopTimeout) for the relay goroutines to stop and then ends the client
// session.
//
// Close is idempotent: only the first call does the work, later or concurrent
// calls wait for it and return its result. Waiting for the relay goroutines
// makes sure no goroutine of this device still feeds or drains the lwIP stack
// when the next device is created (defect N-1).
func (d *RemoteDevice) Close() *perrs.PlatformError {
	d.closeOnce.Do(func() {
		d.closeErr = d.closeOnceInternal()
	})
	return d.closeErr
}

func (d *RemoteDevice) closeOnceInternal() *perrs.PlatformError {
	defer func() {
		if d.client == nil {
			return
		}
		if err := d.client.EndSession(); err != nil {
			slog.Warn("failed to end backend Client session", "err", redact.Err(err))
		}
	}()
	var err error = nil
	d.mu.Lock()
	d.closed = true
	if d.tun != nil {
		err = d.tun.Close()
	}
	d.mu.Unlock()
	// The relay goroutines also close the device when they stop; vpn.RemoteDevice
	// closes the lwIP device only once.
	if d.rd != nil {
		err = errors.Join(err, d.rd.Close())
	}
	d.waitForRelays(relayStopTimeout)
	return perrs.ToPlatformError(err)
}

// waitForRelays waits until the relay goroutines have stopped or timeout
// expires. It must not be called with d.mu held.
func (d *RemoteDevice) waitForRelays(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		d.relays.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		slog.Warn("relay goroutines did not stop in time after closing the remote device", "timeout", timeout)
		return false
	}
}

// goRelay runs relay on a goroutine tracked by d.relays. The caller must hold
// d.mu and have checked that d is not closed.
func (d *RemoteDevice) goRelay(relay func()) {
	d.relays.Add(1)
	go func() {
		defer d.relays.Done()
		relay()
	}()
}

func errRemoteDeviceClosed() *perrs.PlatformError {
	return &perrs.PlatformError{
		Code:    perrs.InternalError,
		Message: "remote device is closed",
	}
}
