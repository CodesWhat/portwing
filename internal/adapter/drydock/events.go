// SPDX-License-Identifier: MIT

package drydock

import (
	"strconv"
	"strings"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// forwardedContainerActions are the Docker actions forwarded as
// dd:container_event, using Docker's own names. kill is deliberately absent:
// a user stop emits kill, die, stop, and Drydock classifies the die from the
// stop that follows it, so die and stop must both pass through undeduplicated.
var forwardedContainerActions = map[string]bool{
	"die":     true,
	"oom":     true,
	"stop":    true,
	"start":   true,
	"restart": true,
}

// ContainerEventFromDocker maps one raw Docker event to a dd:container_event
// payload. It returns false for non-container events and for every action
// outside die, oom, stop, start and restart. There is no coalescing or
// classification here; one forwarded Docker event is one message.
func ContainerEventFromDocker(ev docker.DockerEvent) (protocol.DDContainerEventMessage, bool) {
	// An empty Type comes from older daemons; the stream is already filtered
	// to type=container.
	if ev.Type != "" && ev.Type != "container" {
		return protocol.DDContainerEventMessage{}, false
	}
	action := ev.Action
	if action == "" {
		action = ev.Status
	}
	if !forwardedContainerActions[action] {
		return protocol.DDContainerEventMessage{}, false
	}

	id := ev.Actor.ID
	if id == "" {
		id = ev.ID
	}
	msg := protocol.DDContainerEventMessage{
		ContainerID: id,
		Name:        strings.TrimPrefix(ev.Actor.Attributes["name"], "/"),
		Action:      action,
		Time:        eventTimeMillis(ev),
	}
	if action == "die" {
		if code, err := strconv.Atoi(ev.Actor.Attributes["exitCode"]); err == nil {
			msg.ExitCode = &code
		}
	}
	if action == "oom" {
		oom := true
		msg.OOMKilled = &oom
	}
	return msg, true
}

func eventTimeMillis(ev docker.DockerEvent) int64 {
	if ev.TimeNano != 0 {
		return ev.TimeNano / 1e6
	}
	return ev.Time * 1000
}
