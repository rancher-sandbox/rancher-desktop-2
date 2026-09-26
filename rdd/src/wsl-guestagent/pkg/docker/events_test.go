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

package docker

import (
	"testing"

	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/assert"

	"github.com/rancher-sandbox/rancher-desktop/src/wslproxy"
)

func TestPortMapFromDocker(t *testing.T) {
	ports := nat.PortMap{
		"80/tcp":   {{HostIP: "127.0.0.1", HostPort: "8080"}, {HostIP: "::1", HostPort: "8080"}},
		"53/udp":   {{HostIP: "0.0.0.0", HostPort: "5353"}},
		"9000/tcp": nil,
	}
	want := wslproxy.PortMap{
		"80/tcp":   {{HostIP: "127.0.0.1", HostPort: "8080"}, {HostIP: "::1", HostPort: "8080"}},
		"53/udp":   {{HostIP: "0.0.0.0", HostPort: "5353"}},
		"9000/tcp": {},
	}
	assert.Equal(t, want, portMapFromDocker(ports))
}
