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

package redact

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// All addresses below are documentation/test values (RFC 5737, RFC 3849, RFC 2606).
func TestString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"no address", "context deadline exceeded", "context deadline exceeded"},
		{"ipv4", "server 192.0.2.10 unreachable", "server <redacted> unreachable"},
		{"ipv4 port", "connect to 198.51.100.7:8388 failed", "connect to <redacted> failed"},
		{"dial ipv4", "dial tcp 203.0.113.5:443: connect: connection refused",
			"dial tcp <redacted>: connect: connection refused"},
		{"read udp local remote", "read udp 10.0.0.2:5555->203.0.113.5:443: i/o timeout",
			"read udp <redacted>: i/o timeout"},
		{"dial hostname", "dial tcp proxy.example.com:443: i/o timeout", "dial tcp <redacted>: i/o timeout"},
		{"lookup", "dial tcp: lookup proxy.example.com on 192.0.2.53:53: no such host",
			"dial tcp: lookup <redacted> on <redacted>: no such host"},
		{"lookup no resolver", "lookup proxy.example.net: no such host", "lookup <redacted>: no such host"},
		{"host port", `failed to connect to "proxy.example.org:8443"`, `failed to connect to "<redacted>"`},
		{"bracketed ipv6", "dial tcp [2001:db8::1]:443: connect: network is unreachable",
			"dial tcp <redacted>: connect: network is unreachable"},
		{"bracketed ipv6 in text", "server [2001:db8:0:1::2]:8388 refused", "server <redacted> refused"},
		{"bare ipv6", "no route to 2001:db8::abcd", "no route to <redacted>"},
		{"ipv4 mapped ipv6", "peer ::ffff:192.0.2.1 closed", "peer <redacted> closed"},
		{"ss url", `parse "ss://Y2hhY2hhMjA6ZmFrZQ@192.0.2.1:8388/?outline=1": bad`, `parse "ss://<redacted>": bad`},
		{"ws url", "websocket dial wss://proxy.example.com/tcp failed", "websocket dial wss://<redacted> failed"},
		{"time not ipv6", "took 12:34:56", "took 12:34:56"},
		{"word with colons", "err: proxy: closed", "err: proxy: closed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := String(tc.in)
			if got != tc.want {
				t.Fatalf("String(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestErrNil(t *testing.T) {
	if got := Err(nil); got != nil {
		t.Fatalf("Err(nil) = %v, want nil", got)
	}
}

func TestErrDoesNotChangeError(t *testing.T) {
	base := errors.New("dial tcp 192.0.2.1:443: connect: connection refused")
	err := fmt.Errorf("wrapped: %w", base)
	got := Err(err)
	if got != "wrapped: dial tcp <redacted>: connect: connection refused" {
		t.Fatalf("Err() = %q", got)
	}
	if !errors.Is(err, base) || !strings.Contains(err.Error(), "192.0.2.1:443") {
		t.Fatalf("original error must stay unchanged, got %q", err.Error())
	}
}

func TestErrInSlogAttribute(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Warn("remote device TCP is not healthy",
		"err", Err(errors.New("dial tcp 198.51.100.20:8388: i/o timeout")))
	logger.Warn("remote device UDP is healthy", "err", Err(nil))
	out := buf.String()
	if strings.Contains(out, "198.51.100.20") || strings.Contains(out, "8388") {
		t.Fatalf("log leaks the endpoint: %s", out)
	}
	if !strings.Contains(out, "<redacted>") || !strings.Contains(out, "err=<nil>") {
		t.Fatalf("unexpected log output: %s", out)
	}
}
