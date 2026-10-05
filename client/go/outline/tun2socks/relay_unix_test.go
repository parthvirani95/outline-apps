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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestMakeTunFile_InvalidFD(t *testing.T) {
	_, err := makeTunFile(-1)
	require.Error(t, err)
}

// A pending Read on the TUN file must return once the file is closed,
// otherwise the TUN -> device goroutine (and the dup'ed fd) outlives the VPN.
func TestMakeTunFile_CloseInterruptsRead(t *testing.T) {
	fds := make([]int, 2)
	require.NoError(t, unix.Pipe(fds))
	// The writer end stays open, so a Read on the reader end blocks.
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])

	tun, err := makeTunFile(fds[0])
	require.NoError(t, err)

	// Use SyscallConn instead of Fd(): Fd() would switch the file back to blocking mode.
	rc, err := tun.SyscallConn()
	require.NoError(t, err, "TUN file must be pollable")
	var flags int
	var flagsErr error
	require.NoError(t, rc.Control(func(fd uintptr) {
		flags, flagsErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}))
	require.NoError(t, flagsErr)
	require.NotZero(t, flags&unix.O_NONBLOCK, "dup'ed TUN fd must be non-blocking")

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1500)
		_, err := tun.Read(buf)
		readDone <- err
	}()

	// Give the reader time to block in Read.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-readDone:
		t.Fatalf("Read returned before Close: %v", err)
	default:
	}

	require.NoError(t, tun.Close())
	select {
	case err := <-readDone:
		require.True(t, errors.Is(err, os.ErrClosed), "unexpected Read error: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("Close did not interrupt the pending Read within 1s")
	}
}
