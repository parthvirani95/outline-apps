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

package vpn

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// panicOnDoubleCloseDevice behaves like the outline-sdk lwIP device when it is
// closed twice: the second close of its channel panics.
type panicOnDoubleCloseDevice struct {
	done    chan struct{}
	closes  atomic.Int32
	payload []byte
}

func (d *panicOnDoubleCloseDevice) Close() error {
	d.closes.Add(1)
	close(d.done)
	return errors.New("close result")
}
func (d *panicOnDoubleCloseDevice) MTU() int                    { return 1500 }
func (d *panicOnDoubleCloseDevice) Read(p []byte) (int, error)  { <-d.done; return 0, io.EOF }
func (d *panicOnDoubleCloseDevice) Write(p []byte) (int, error) { return len(p), nil }
func (d *panicOnDoubleCloseDevice) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(d.payload)
	return int64(n), err
}

func TestOnceCloseDevice_ConcurrentClose(t *testing.T) {
	inner := &panicOnDoubleCloseDevice{done: make(chan struct{})}
	dev := &RemoteDevice{ReadWriteCloser: &onceCloseDevice{IPDevice: inner}}

	var wg sync.WaitGroup
	errs := make([]error, 16)
	for i := range errs {
		wg.Go(func() {
			if i%2 == 0 {
				errs[i] = dev.Close()
			} else {
				// RelayTraffic closes its destination when the copy ends.
				RelayTraffic(dev, bytes.NewReader(nil))
				errs[i] = dev.Close()
			}
		})
	}
	wg.Wait()

	require.Equal(t, int32(1), inner.closes.Load())
	for _, err := range errs {
		require.EqualError(t, err, "close result")
	}
}

func TestOnceCloseDevice_WriteToIsDelegated(t *testing.T) {
	inner := &panicOnDoubleCloseDevice{done: make(chan struct{}), payload: []byte("packet")}
	var dev io.Reader = &onceCloseDevice{IPDevice: inner}

	_, ok := dev.(io.WriterTo)
	require.True(t, ok)
	var out bytes.Buffer
	n, err := io.Copy(&out, dev)
	require.NoError(t, err)
	require.Equal(t, int64(6), n)
	require.Equal(t, "packet", out.String())
}
