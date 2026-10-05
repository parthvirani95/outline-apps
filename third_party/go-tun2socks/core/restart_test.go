package core

// PATCH (N-1): regression test for the UDP listener use-after-free on stack
// restart. See PATCHES.md.

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// connCollector is a UDP handler that remembers every connection it sees, so
// that other goroutines can send replies on them, including stale ones that
// belong to an already closed stack.
type connCollector struct {
	mu    sync.Mutex
	conns []UDPConn // ring of the most recent connections
	seen  int
}

func (h *connCollector) Connect(conn UDPConn, target *net.UDPAddr) error {
	return nil
}

// ReceiveTo may run under lwipMutex (input -> udp_input -> udpRecvFn), so it must
// not call WriteFrom synchronously.
func (h *connCollector) ReceiveTo(conn UDPConn, data []byte, addr *net.UDPAddr) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.conns) < 1024 {
		h.conns = append(h.conns, conn)
	} else {
		h.conns[h.seen%len(h.conns)] = conn
	}
	h.seen++
	return nil
}

// snapshot returns a copy of the remembered connections and the number seen.
// Connect goroutines of the core may still call ReceiveTo after the test's own
// goroutines have stopped, so all access goes through h.mu.
func (h *connCollector) snapshot() ([]UDPConn, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]UDPConn(nil), h.conns...), h.seen
}

func (h *connCollector) pick(i int) UDPConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.conns) == 0 {
		return nil
	}
	return h.conns[i%len(h.conns)]
}

// udpPacketFromPort returns a copy of the NTP test packet with the UDP source
// port replaced, so that every port creates a new udpConn.
func udpPacketFromPort(port uint16) []byte {
	pkt := decode(ntpHex)
	binary.BigEndian.PutUint16(pkt[ipv4Header:ipv4Header+2], port)
	// Zero UDP checksum (= no checksum for IPv4).
	pkt[ipv4Header+6], pkt[ipv4Header+7] = 0, 0
	return pkt
}

// TestStackRestartUDPRace starts and closes the lwIP stack many times while
// other goroutines feed UDP packets into the current stack and send replies on
// both live and stale UDP connections, like the outline-sdk PacketRelay
// goroutines do on a device restart. Before the N-1 patch, replies could reach
// udp_sendto with the freed listener pcb (use-after-free, SIGBUS/SIGSEGV in
// udp_input on Android). Run with -race.
func TestStackRestartUDPRace(t *testing.T) {
	const iterations = 200

	// The module targets go1.13, so the typed atomics are not available.
	var outputs, replies int64
	var stop int32
	var nextPort uint32 = 10000
	var curMu sync.Mutex
	var current LWIPStack
	getCurrent := func() LWIPStack {
		curMu.Lock()
		defer curMu.Unlock()
		return current
	}
	setCurrent := func(s LWIPStack) {
		curMu.Lock()
		current = s
		curMu.Unlock()
	}
	stopped := func() bool { return atomic.LoadInt32(&stop) != 0 }

	RegisterOutputFn(func(b []byte) (int, error) {
		atomic.AddInt64(&outputs, 1)
		return len(b), nil
	})
	h := &connCollector{}
	RegisterUDPConnHandler(h)

	var wg sync.WaitGroup

	// Feeders: write UDP packets with fresh source ports into the current stack.
	for f := 0; f < 2; f++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stopped() {
				s := getCurrent()
				if s == nil {
					time.Sleep(10 * time.Microsecond)
					continue
				}
				port := uint16(10000 + atomic.AddUint32(&nextPort, 1)%50000)
				_, _ = s.Write(udpPacketFromPort(port))
			}
		}()
	}

	// Repliers: send replies on remembered connections, live or stale.
	payload := []byte("reply payload")
	from := &net.UDPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 53}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := r; !stopped(); i += 7 {
				conn := h.pick(i)
				if conn == nil {
					time.Sleep(10 * time.Microsecond)
					continue
				}
				if _, err := conn.WriteFrom(payload, from); err == nil {
					atomic.AddInt64(&replies, 1)
				}
			}
		}(r)
	}

	for i := 0; i < iterations; i++ {
		s := NewLWIPStack()
		setCurrent(s)
		time.Sleep(time.Duration(200+i%5*200) * time.Microsecond)
		setCurrent(nil)

		// Close twice, concurrently: Close must be idempotent.
		var cwg sync.WaitGroup
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			if err := s.Close(); err != nil {
				t.Errorf("concurrent Close: %v", err)
			}
		}()
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		cwg.Wait()

		// A closed stack must reject input.
		if _, err := s.Write(udpPacketFromPort(9)); err == nil {
			t.Fatal("Write on a closed stack succeeded")
		}
	}

	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	if atomic.LoadInt64(&replies) == 0 {
		t.Error("no reply reached udp_sendto; the test did not exercise WriteFrom")
	}
	conns, seen := h.snapshot()
	t.Logf("iterations=%d replies=%d outputs=%d conns=%d", iterations, atomic.LoadInt64(&replies), atomic.LoadInt64(&outputs), seen)

	// Every remembered connection belongs to a closed stack now.
	for _, conn := range conns {
		if _, err := conn.WriteFrom(payload, from); err == nil {
			t.Fatal("WriteFrom on a connection of a closed stack succeeded")
		}
	}
}

// TestUDPWriteFromLiveStack checks that a reply on a connection of the live
// stack still reaches the output function.
func TestUDPWriteFromLiveStack(t *testing.T) {
	out := make(chan []byte, 1)
	RegisterOutputFn(func(b []byte) (int, error) {
		select {
		case out <- append([]byte(nil), b...):
		default:
		}
		return len(b), nil
	})
	h := &connCollector{}
	RegisterUDPConnHandler(h)

	s := NewLWIPStack()
	defer s.Close()
	if _, err := s.Write(udpPacketFromPort(4242)); err != nil {
		t.Fatal(err)
	}
	// Connect runs asynchronously; the handler sees the first packet once the
	// connection is connected.
	deadline := time.Now().Add(2 * time.Second)
	conn := h.pick(0)
	for ; conn == nil; conn = h.pick(0) {
		if time.Now().After(deadline) {
			t.Fatal("no UDP connection created")
		}
		time.Sleep(time.Millisecond)
	}

	payload := []byte("hello")
	from := &net.UDPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 53}
	for {
		_, err := conn.WriteFrom(payload, from)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("WriteFrom on the live stack failed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case pkt := <-out:
		if len(pkt) != ipv4Header+udpHeader+len(payload) {
			t.Fatalf("unexpected output packet length %d", len(pkt))
		}
		if got := binary.BigEndian.Uint16(pkt[ipv4Header+2 : ipv4Header+4]); got != 4242 {
			t.Fatalf("reply sent to port %d, want 4242", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reply did not reach the output function")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.WriteFrom(payload, from); err == nil {
		t.Fatal("WriteFrom after Close succeeded")
	}
}
