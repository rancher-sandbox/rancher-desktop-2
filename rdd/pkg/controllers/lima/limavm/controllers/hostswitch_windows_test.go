// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"net"
	"testing"

	"gotest.tools/v3/assert"
)

func TestValidateSubnet(t *testing.T) {
	t.Run("default subnet", func(t *testing.T) {
		s, err := validateSubnet("192.168.127.0/24")
		assert.NilError(t, err)
		assert.Equal(t, s.GatewayIP, "192.168.127.1")
		assert.Equal(t, s.StaticDNSHost, "192.168.127.254")
		assert.Equal(t, s.SubnetCIDR, "192.168.127.0/24")
		assert.Equal(t, len(s.StaticDHCPLease), 1)
		assert.Equal(t, s.StaticDHCPLease["192.168.127.2"], tapDeviceMacAddr)
	})

	t.Run("custom subnet", func(t *testing.T) {
		s, err := validateSubnet("10.0.0.0/24")
		assert.NilError(t, err)
		assert.Equal(t, s.GatewayIP, "10.0.0.1")
		assert.Equal(t, s.StaticDNSHost, "10.0.0.254")
		assert.Equal(t, s.SubnetCIDR, "10.0.0.0/24")
		assert.Equal(t, s.StaticDHCPLease["10.0.0.2"], tapDeviceMacAddr)
	})

	t.Run("invalid CIDR", func(t *testing.T) {
		_, err := validateSubnet("not-a-cidr")
		assert.ErrorContains(t, err, "invalid subnet")
	})

	t.Run("IPv6 rejected", func(t *testing.T) {
		_, err := validateSubnet("fd00::/64")
		assert.ErrorContains(t, err, "not IPv4")
	})
}

func TestNewVirtualNetworkConfig(t *testing.T) {
	subnet := hostSwitchSubnet{
		GatewayIP:       "192.168.127.1",
		StaticDHCPLease: map[string]string{"192.168.127.2": tapDeviceMacAddr},
		StaticDNSHost:   "192.168.127.254",
		SubnetCIDR:      "192.168.127.0/24",
	}
	cfg := newVirtualNetworkConfig(subnet)

	assert.Equal(t, cfg.MTU, defaultMTU)
	assert.Equal(t, cfg.Subnet, "192.168.127.0/24")
	assert.Equal(t, cfg.GatewayIP, "192.168.127.1")
	assert.Equal(t, cfg.GatewayMacAddress, gatewayMacAddr)
	assert.DeepEqual(t, cfg.DHCPStaticLeases, map[string]string{"192.168.127.2": tapDeviceMacAddr})

	// DNS zones
	assert.Equal(t, len(cfg.DNS), 2)
	assert.Equal(t, cfg.DNS[0].Name, "rancher-desktop.internal.")
	assert.Equal(t, cfg.DNS[1].Name, "docker.internal.")
	for _, zone := range cfg.DNS {
		assert.Equal(t, len(zone.Records), 2)
		assert.Equal(t, zone.Records[0].Name, "gateway")
		assert.Assert(t, zone.Records[0].IP.Equal(net.ParseIP("192.168.127.1")))
		assert.Equal(t, zone.Records[1].Name, "host")
		assert.Assert(t, zone.Records[1].IP.Equal(net.ParseIP("192.168.127.254")))
	}

	// NAT and virtual IPs
	assert.DeepEqual(t, cfg.NAT, map[string]string{"192.168.127.254": "127.0.0.1"})
	assert.DeepEqual(t, cfg.GatewayVirtualIPs, []string{"192.168.127.254"})
}
