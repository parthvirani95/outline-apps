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

//go:build unix

package tun2socks

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"localhost/client/go/outline/vpn"
)

// fakeIPDevice stands in for the lwIP device: Read blocks until Close.
type fakeIPDevice struct {
	closeOnce sync.Once
	done      chan struct{}
	closes    atomic.Int32
	// unblockOnClose=false simulates a device whose Read never returns.
	unblockOnClose bool
}

func newFakeIPDevice(unblockOnClose bool) *fakeIPDevice {
	return &fakeIPDevice{done: make(chan struct{}), unblockOnClose: unblockOnClose}
}

func (d *fakeIPDevice) Read(p []byte) (int, error) {
	<-d.done
	return 0, io.EOF
}

func (d *fakeIPDevice) Write(p []byte) (int, error) { return len(p), nil }

func (d *fakeIPDevice) Close() error {
	d.closes.Add(1)
	if d.unblockOnClose {
		d.closeOnce.Do(func() { close(d.done) })
	}
	return nil
}

func newTestRemoteDevice(dev io.ReadWriteCloser) *RemoteDevice {
	// client is nil: Close skips EndSession.
	return &RemoteDevice{rd: &vpn.RemoteDevice{ReadWriteCloser: dev}}
}

func newTestTunFD(t *testing.T) int {
	fds := make([]int, 2)
	require.NoError(t, unix.Pipe(fds))
	t.Cleanup(func() {
		unix.Close(fds[0])
		unix.Close(fds[1])
	})
	// The writer end stays open, so a Read on the reader end blocks until the
	// dup'ed TUN file is closed.
	return fds[0]
}

func relaysStopped(d *RemoteDevice) bool {
	done := make(chan struct{})
	go func() {
		d.relays.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func TestRemoteDeviceClose_WaitsForRelaysAndIsIdempotent(t *testing.T) {
	dev := newFakeIPDevice(true)
	d := newTestRemoteDevice(dev)
	require.Nil(t, GoRelayTraffic(newTestTunFD(t), d))
	require.False(t, relaysStopped(d), "relay goroutines should be running")

	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Go(func() {
			if err := d.Close(); err != nil {
				results[i] = err
			}
		})
	}
	wg.Wait()
	for _, err := range results {
		require.NoError(t, err)
	}

	require.True(t, relaysStopped(d), "Close returned before the relay goroutines stopped")
	// Close of the owner plus the relay goroutine's EOF close; the remote device
	// itself did the work once.
	require.GreaterOrEqual(t, dev.closes.Load(), int32(1))

	// No relay can start on a closed device.
	perr := GoRelayTraffic(newTestTunFD(t), d)
	require.NotNil(t, perr)
	require.True(t, relaysStopped(d))

	// Further Close calls are no-ops.
	require.Nil(t, d.Close())
}

func TestRemoteDeviceClose_RelayWaitIsBounded(t *testing.T) {
	saved := relayStopTimeout
	relayStopTimeout = 200 * time.Millisecond
	t.Cleanup(func() { relayStopTimeout = saved })

	// A device whose Read never returns keeps the device -> TUN goroutine alive.
	dev := newFakeIPDevice(false)
	d := newTestRemoteDevice(dev)
	require.Nil(t, GoRelayTraffic(newTestTunFD(t), d))

	start := time.Now()
	require.Nil(t, d.Close())
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, relayStopTimeout)
	require.Less(t, elapsed, relayStopTimeout+2*time.Second)

	// Let the stuck goroutine finish so the test does not leak it.
	close(dev.done)
	require.True(t, relaysStopped(d))
}
