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

package configregistry

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutlineDNSResolvers(t *testing.T) {
	require.ElementsMatch(t, []netip.AddrPort{
		netip.MustParseAddrPort("1.1.1.1:53"),
		netip.MustParseAddrPort("9.9.9.9:53"),
	}, outlineDNSResolvers)
}

// OpenDNS answers REFUSED from some regions (e.g. France), so it must not be used.
func TestOutlineDNSResolvers_NoOpenDNS(t *testing.T) {
	openDNS := netip.MustParsePrefix("208.67.216.0/21")
	for _, r := range outlineDNSResolvers {
		require.False(t, openDNS.Contains(r.Addr()), "OpenDNS resolver %v must not be used", r)
	}
}
