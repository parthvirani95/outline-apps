# Local patches to go-tun2socks v1.16.11

This directory is `github.com/eycorsican/go-tun2socks@v1.16.11` copied from the Go module cache
(LICENSE kept) and patched. The root `go.mod` points the module here:

```
replace github.com/eycorsican/go-tun2socks => ./third_party/go-tun2socks
```

Every patched spot in the code is marked `PATCH (N-1)`. To upgrade, copy the new upstream version
over this directory, re-apply the changes below, and run the tests listed at the end.

## Why: defect N-1

Android (OnePlus, arm64) crashed with `SIGBUS BUS_ADRALN` in `udp_input+860`: lwIP called
`pcb->recv` through a garbage pointer. It happened after several live re-applies (each one closes
the remote device and creates a new one) followed by a disconnect.

lwIP in this library is a process-wide singleton. `NewLWIPStack` creates one UDP listener pcb, and
the patched `udp_input` (`core/c/core/udp.c`) sends every datagram to the first pcb in `udp_pcbs`
and calls its `recv`. With `MEM_LIBC_MALLOC`/`MEMP_MEM_MALLOC` the pcb memory is plain libc memory.
Upstream had these races:

- `udpConn.WriteFrom` called `udp_sendto(conn.pcb, ...)` without `lwipMutex`, checking only the
  connection state. It runs on outline-sdk PacketRelay goroutines, concurrently with `input()` and
  `lwipStack.Close`. A late reply could use the listener pcb after `udp_remove` freed it, and
  `udp_sendto` can bind the pcb again, which puts the freed memory back at the head of `udp_pcbs`.
  The next `udp_input` then jumps through garbage.
- `lwipStack.Close` closed the UDP connections without `lwipMutex`, so a `udpConn` could be created
  by `input()` during `Close` and stay open after the pcb was freed.
- `input()` did not re-check, after taking `lwipMutex`, whether the stack had been closed while it
  waited for the lock.
- `lwipStack.Close` was not idempotent: a second call ran `udp_remove` on the freed pcb again.

Reproduction: `TestStackRestartUDPRace` (below) against the unpatched module crashed in all 5 runs on
macOS arm64 (4 times with `SIGBUS` in `_Cfunc_input` while another goroutine was in
`_Cfunc_udp_sendto`, once killed by the OS), which matches the device crash. The test must
skip the double `Close` on the unpatched module, because that is a separate double free.

## Changes

1. `core/lwip.go`
   - New package variable `liveUDPPCB`: the UDP listener pcb of the open stack. `NewLWIPStack`
     sets it, `Close` clears it. It is only read or written while `lwipMutex` is held.
   - New field `lwipStack.closed`, only read or written while `lwipMutex` is held.
   - `NewLWIPStack` creates the TCP/UDP listener pcbs while holding `lwipMutex`. Goroutines of the
     previous stack may still be inside lwIP.
   - `Close` is idempotent. While holding `lwipMutex` it sets `closed`, clears `liveUDPPCB` and
     closes every `udpConn`. Then it aborts the TCP connections outside the lock, because
     `tcpConn.Abort` takes `lwipMutex` itself. Finally, while holding `lwipMutex` again, it removes
     the callbacks and frees the listener pcbs, as upstream did.
2. `core/input.go`: `input()` takes the stack. After locking `lwipMutex` it drops the packet and
   returns an error if the stack is closed.
3. `core/udp_conn.go`: `udpConn.WriteFrom` holds `lwipMutex` for the state check, the address
   conversion (`ipaddr_aton`), `pbuf_alloc_reference`, `udp_sendto` and `pbuf_free`. It returns an
   error if the connection is closed or `conn.pcb != liveUDPPCB`. It also handles a nil address
   and a failed `pbuf` allocation.
4. `core/handler.go`, `core/output.go`: `RegisterTCPConnHandler`, `RegisterUDPConnHandler` and
   `RegisterOutputFn` take `lwipMutex`. lwIP callbacks read these values while holding
   `lwipMutex`, and the outline-sdk registers new ones on every device restart.
5. `core/errors.go`, `core/tcp_conn.go`: `go vet` fixes only. `lwipError.Error` uses
   `strconv.Itoa` instead of `string(int)`, and two unreachable `return nil` lines are removed.
6. `core/restart_test.go`: new regression tests, listed below.

## Deadlock audit (lwipMutex is not re-entrant)

These paths run while `lwipMutex` is held. None of them can reach `WriteFrom`, `Close`,
`NewLWIPStack`, `Register*` or `input()`, so the new lock sites cannot self-deadlock:

- `input()` → `udp_input` → `udpRecvFn` → `newUDPConn`, which only starts a goroutine for
  `handler.Connect`, then `udpConn.ReceiveTo` → `enqueueEarlyPacket`/`checkState`, which take only
  the per-connection mutex, then `UDPConnHandler.ReceiveTo`. In outline-sdk v0.1.0-rc1 that is
  `lwip2transport.udpRelayHandler.ReceiveTo` → `PacketSender.SendPacket`. Every sender the fork
  uses (`packetrelay` listener/timeout/delegate relays, `dnsintercept`, `dnstruncate`) only writes
  to a socket or a buffered channel, or starts a goroutine. Replies always arrive on a separate
  `ReceivePackets` goroutine (`runReceive`/`runDNSReceiver` → `HandlePacket` → `WriteFrom`). The
  legacy `udpHandler` (PacketProxy) works the same way, and the fork does not use it.
- `input()`/`udp_sendto`/TCP output → `output()` → `OutputFn` (outline-sdk
  `forwardOutgoingIPPacket`): channels only. It blocks until the device's reader takes the packet,
  or until the device's `done` channel is closed. outline-sdk closes `done` before it calls
  `stack.Close`, so a blocked output always ends. The reader writes to the TUN and never takes
  `lwipMutex`. Upstream TCP output already worked this way.
- `sys_check_timeouts` (timer goroutine) → TCP callbacks and output. These are upstream paths and
  this patch does not change them.
- `lwipStack.Close` → `udpConn.Close`: takes only the per-connection mutex and `udpConns.Delete`
  (allowed inside `sync.Map.Range`).
- `WriteFrom` itself → `checkState` (per-connection mutex), `ipaddr_aton`, `udp_sendto` →
  `output()` as above. `LWIP_NETIF_LOOPBACK` is 0, so `udp_sendto` never calls a `recv` callback
  synchronously.

Lock order is always `lwipMutex` → `udpConn.Mutex` (`WriteFrom`, `Close`, `udpRecvFn`). No path
takes `udpConn.Mutex` and then `lwipMutex`. `tcpConn.Abort` takes `lwipMutex`, so `Close` calls it
without holding the lock.

Rule for future callers: never call `UDPConn.WriteFrom` (or any locking function in this package)
from a `UDPConnHandler`/`TCPConnHandler` callback or from the output function. Those run under
`lwipMutex`.

## Tests

From the repository root (the `replace` makes the root module build this copy):

```
go vet github.com/eycorsican/go-tun2socks/core
go test -race -count=1 -v github.com/eycorsican/go-tun2socks/core
```

- `TestStackRestartUDPRace` opens and closes the stack 200 times, closing each one twice
  concurrently. Meanwhile two goroutines feed UDP packets with new source ports and four goroutines
  send replies on live and stale connections.
- `TestUDPWriteFromLiveStack` checks that a reply on a live connection still reaches the output
  function, and that `WriteFrom` fails after `Close`.
