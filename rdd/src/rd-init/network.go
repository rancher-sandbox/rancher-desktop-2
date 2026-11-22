// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	// Path to cloud-config style networking configuration file provided by Lima.
	networkConfigPath = "/mnt/lima-cidata/network-config"
)

var networkConfig struct {
	Version   uint `yaml:"version"`
	Ethernets map[string]struct {
		Match struct {
			MACAddress string `yaml:"macaddress"`
		} `yaml:"match"`
		DHCP4          bool   `yaml:"dhcp4"`
		SetName        string `yaml:"set-name,omitempty"`
		DHCP4Overrides struct {
			RouteMetric uint `yaml:"route-metric"`
		} `yaml:"dhcp4-overrides"`
		DHCPIdentifier string `yaml:"dhcp-identifier"`
		NameServers    struct {
			Addresses []net.IP `yaml:"addresses"`
		} `yaml:"nameservers"`
	} `yaml:"ethernets"`
}

// renameInterface gives the interface with the given MAC address the name
// newName. If that interface hasn't appeared yet, renameInterface leaves it
// for udev to name from the .link file.
func renameInterface(ctx context.Context, macAddress, newName string) error {
	mac, err := net.ParseMAC(macAddress)
	if err != nil {
		return fmt.Errorf("failed to parse MAC address %q: %w", macAddress, err)
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to list network interfaces: %w", err)
	}
	for _, iface := range interfaces {
		if !bytes.Equal(iface.HardwareAddr, mac) {
			continue
		}
		if iface.Name == newName {
			return nil
		}
		// udev applies a .link file's Name= only when an interface is added,
		// and the distro's naming scheme stops udev from renaming an
		// interface a second time. So udev can't apply the .link file to an
		// interface that already exists, and rd-init renames it here. The
		// kernel refuses to rename an interface that is up, so take it down
		// first. It stays down until networkd starts, and networkd brings it
		// up because the .network file for it matches its MAC address.
		slog.InfoContext(ctx, "renaming network interface", "from", iface.Name, "to", newName)
		if err := runCommand(ctx, "/usr/bin/ip", "link", "set", "dev", iface.Name, "down"); err != nil {
			return fmt.Errorf("failed to bring down network interface %s: %w", iface.Name, err)
		}
		if err := runCommand(ctx, "/usr/bin/ip", "link", "set", "dev", iface.Name, "name", newName); err != nil {
			return fmt.Errorf("failed to rename network interface %s to %s: %w", iface.Name, newName, err)
		}
		return nil
	}
	slog.InfoContext(ctx, "network interface has not appeared yet; udev names it when it does", "mac", macAddress, "name", newName)
	return nil
}

func LoadNetworkConfig(ctx context.Context) ([]string, error) {
	hasChanges := false
	file, err := os.Open(networkConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read network-config: %w", err)
	}
	defer file.Close()

	if err := yaml.NewDecoder(file).DecodeContext(ctx, &networkConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal network-config: %w", err)
	}

	for name, ethernet := range networkConfig.Ethernets {
		if ethernet.SetName != "" {
			if ethernet.Match.MACAddress == "" {
				return nil, fmt.Errorf("network interface %q has set-name=%q but no match", name, ethernet.SetName)
			}
			contents := []string{
				"[Match]",
				fmt.Sprintf("MACAddress=%s", ethernet.Match.MACAddress),
				"[Link]",
				"NamePolicy=",
				fmt.Sprintf("Name=%s", ethernet.SetName),
				"",
			}
			outPath := fmt.Sprintf("/run/systemd/network/10-%s.link", name)
			if err := os.WriteFile(outPath, []byte(strings.Join(contents, "\n")), 0o644); err != nil {
				return nil, fmt.Errorf("failed to write link file %s: %w", outPath, err)
			}
			if err := renameInterface(ctx, ethernet.Match.MACAddress, ethernet.SetName); err != nil {
				return nil, err
			}
			hasChanges = true
		}

		config := make(map[string]map[string]string)
		update := func(section, key, value string) {
			m, ok := config[section]
			if !ok {
				m = make(map[string]string)
				config[section] = m
			}
			m[key] = value
		}

		if ethernet.Match.MACAddress != "" {
			update("Match", "MACAddress", ethernet.Match.MACAddress)
		}
		if ethernet.DHCP4 {
			update("Network", "DHCP", "ipv4")
		}
		if ethernet.DHCP4Overrides.RouteMetric != 0 {
			update("DHCPv4", "RouteMetric", fmt.Sprintf("%d", ethernet.DHCP4Overrides.RouteMetric))
		}
		if ethernet.DHCPIdentifier != "" {
			switch ethernet.DHCPIdentifier {
			case "mac", "duid":
				update("DHCPv4", "ClientIdentifier", ethernet.DHCPIdentifier)
			default:
				return nil, fmt.Errorf("error writing network-config: ethernet %q has invalid DHCP client identifier %q", name, ethernet.DHCPIdentifier)
			}
		}
		if len(ethernet.NameServers.Addresses) > 0 {
			var addrs []string
			for _, addr := range ethernet.NameServers.Addresses {
				addrs = append(addrs, addr.String())
			}
			update("Network", "DNS", strings.Join(addrs, " "))
		}

		if len(config) == 0 {
			continue
		}

		slog.InfoContext(ctx, "configuring network interface", "interface", name)

		builder := &strings.Builder{}
		for section, items := range config {
			if _, err := fmt.Fprintf(builder, "[%s]\n", section); err != nil {
				return nil, err
			}
			for k, v := range items {
				if _, err := fmt.Fprintf(builder, "%s=%s\n", k, v); err != nil {
					return nil, err
				}
			}
		}
		outPath := fmt.Sprintf("/run/systemd/network/%s.network", name)
		if err := os.WriteFile(outPath, []byte(builder.String()), 0o644); err != nil {
			return nil, fmt.Errorf("failed to write %q network config: %w", name, err)
		}
		hasChanges = true
	}

	if hasChanges {
		return []string{"systemd-networkd.service"}, nil
	}
	return nil, nil
}
