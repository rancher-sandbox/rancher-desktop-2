// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

var containerIDValidator = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// handleLogs is the handler for the `/passthrough/mock/logs/{container}`
// endpoint.  It exists for providing fake logs for testing and screenshots.
func (c *controller) handleLogs(w http.ResponseWriter, r *http.Request) {
	log := ctrl.LoggerFrom(r.Context())
	containerID, _, _ := strings.Cut(strings.TrimLeft(r.URL.Path, "/"), "/")
	log.Info("Handling logs request", "container", containerID)

	if !containerIDValidator.MatchString(containerID) {
		log.V(5).Info("Invalid container ID", "container", containerID)
		http.Error(w, "Invalid container ID", http.StatusBadRequest)
		return
	}

	var container v1alpha1.Container
	err := c.mgr.GetClient().Get(r.Context(), types.NamespacedName{
		Namespace: apiNamespace,
		Name:      containerID,
	}, &container)
	if err != nil {
		log.V(5).Info("Failed to get container", "error", err)
		http.Error(w, "Failed to get container", http.StatusNotFound)
		return
	}

	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.V(5).Info("Failed to upgrade to WebSocket", "error", err)
		return
	}
	defer conn.Close()
	defer func() {
		message := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Closing connection")
		err := conn.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second))
		if err != nil {
			log.V(5).Info("Failed to close WebSocket", "error", err)
		}
	}()

	// Write a message per line to simulate the real logs better.
	write := func(line string) error {
		err := conn.WriteMessage(websocket.BinaryMessage, []byte(line))
		if err != nil {
			log.V(5).Info("Failed to write WebSocket message", "error", err)
		}
		return err
	}

	// We don't have any logs for the mock controller; just print out some
	// stuff to show that we found the container.
	if err := write(fmt.Sprintf("Logs for container %s\n", containerID)); err != nil {
		return
	}
	keys := slices.Sorted(maps.Keys(container.Status.Labels))
	for _, k := range keys {
		err := write(fmt.Sprintf("Label: %s\t%s\n", k, container.Status.Labels[k]))
		if err != nil {
			return
		}
	}
}
