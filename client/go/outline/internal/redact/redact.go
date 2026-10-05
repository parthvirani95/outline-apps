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

// Package redact removes network endpoints from text that is about to be logged.
//
// It is meant for log attributes only: callers keep returning the original error values (and their
// platform error codes) unchanged, and use [Err] or [String] only where the text goes to a log line,
// so a proxy server's address (IP literal, host:port, transport URL) never reaches the logs.
package redact

import (
	"net/netip"
	"regexp"
)

// Placeholder replaces every redacted endpoint.
const Placeholder = "<redacted>"

var (
	// Transport/config URLs, e.g. "ss://<userinfo>@host:port/?prefix=..." or "wss://host/path". The
	// scheme is kept so the log still says what kind of transport failed.
	urlRe = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9+.-]*://)[^\s"'<>]+`)
	// Go net.OpError addresses: "dial tcp ADDR: ...", "read udp LOCAL->REMOTE: ...".
	opAddrRe = regexp.MustCompile(`\b((?:dial|read|write|listen|accept) (?:tcp|udp|ip)[46]? )([^\s]+?)(:\s|$)`)
	// DNS lookups of the server name: "lookup proxy.example.com on ...: no such host".
	lookupRe = regexp.MustCompile(`\b(lookup )([^\s:<]+)`)
	// Bracketed IPv6 literal with optional port: "[2001:db8::1]:443".
	bracketV6Re = regexp.MustCompile(`\[[0-9A-Fa-f:.%]+(?:%[0-9A-Za-z._-]+)?\](?::\d{1,5})?`)
	// Domain name with a port: "proxy.example.com:443".
	hostPortRe = regexp.MustCompile(`\b(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z][A-Za-z0-9-]*:\d{1,5}\b`)
	// IPv4 literal with optional port: "192.0.2.1", "192.0.2.1:443". Applied after the IPv6 pass so
	// "::ffff:192.0.2.1" is redacted as a whole.
	ipv4Re = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	// Candidate bare IPv6 literal (validated with netip before redacting), incl. "::ffff:192.0.2.1".
	ipv6Re = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:\.\d{1,3}){0,3}`)
)

// String returns s with IP literals, host:port pairs, dial targets and URLs replaced by
// [Placeholder].
func String(s string) string {
	if s == "" {
		return s
	}
	s = urlRe.ReplaceAllString(s, "${1}"+Placeholder)
	s = opAddrRe.ReplaceAllString(s, "${1}"+Placeholder+"${3}")
	s = lookupRe.ReplaceAllString(s, "${1}"+Placeholder)
	s = bracketV6Re.ReplaceAllString(s, Placeholder)
	s = hostPortRe.ReplaceAllString(s, Placeholder)
	s = ipv6Re.ReplaceAllStringFunc(s, func(m string) string {
		if addr, err := netip.ParseAddr(m); err == nil && addr.Is6() {
			return Placeholder
		}
		return m
	})
	s = ipv4Re.ReplaceAllString(s, Placeholder)
	return s
}

// Err returns the redacted text of err for a log attribute, or nil when err is nil (so the log
// line still shows "err=<nil>"). It does not change err itself.
func Err(err error) any {
	if err == nil {
		return nil
	}
	return String(err.Error())
}
