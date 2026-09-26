/*
Copyright © 2026 SUSE LLC
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wslproxy

import (
	"fmt"
	"strconv"
	"strings"
)

// Port is a port number and protocol, such as "80/tcp".
type Port string

// PortBinding is a host IP address and port bound to a Port.
type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// PortMap holds the host bindings of each Port.
type PortMap map[Port][]PortBinding

// NewPort returns the Port for a protocol and a port number.
func NewPort(proto, port string) (Port, error) {
	number, err := parsePortNumber(port)
	if err != nil {
		return "", err
	}
	return Port(fmt.Sprintf("%d/%s", number, proto)), nil
}

// Proto returns the protocol of p, or "tcp" when p has none.
func (p Port) Proto() string {
	_, proto, _ := strings.Cut(string(p), "/")
	if proto == "" {
		return "tcp"
	}
	return proto
}

// Port returns the port number of p.
func (p Port) Port() string {
	port, _, _ := strings.Cut(string(p), "/")
	return port
}

// ParsePort parses a port number from 0 to 65535, and returns 0 for an
// empty string.
func ParsePort(rawPort string) (int, error) {
	if rawPort == "" {
		return 0, nil
	}
	return parsePortNumber(rawPort)
}

func parsePortNumber(rawPort string) (int, error) {
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q: want a number from 0 to 65535", rawPort)
	}
	return port, nil
}
