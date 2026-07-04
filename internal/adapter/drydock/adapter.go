package drydock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

// Label constants for Drydock container configuration.
const (
	LabelWatch        = "dd.watch"
	LabelTagInclude   = "dd.tag.include"
	LabelTagExclude   = "dd.tag.exclude"
	LabelTagTransform = "dd.tag.transform"
	LabelDisplayName  = "dd.display.name"
	LabelDisplayIcon  = "dd.display.icon"
	LabelGroup        = "dd.group"
	LabelLinkTemplate = "dd.link.template"

	defaultMessageHandlerConcurrency = 32
)

// Adapter is the Drydock adapter for Portwing. It provides container sync,
// component sync, watcher/trigger stubs, and SSE broadcasting.
type Adapter struct {
	containers   *adapter.ContainerManager
	sse          *SSEBroadcaster
	dockerClient *docker.Client

	messageSem chan struct{}
	semInit    sync.Once
}

// NewAdapter creates a Drydock adapter. info carries the static agent
// runtime details reported to the controller in the dd:ack event.
func NewAdapter(dockerClient *docker.Client, agentName string, info AgentInfo) *Adapter {
	cm := adapter.NewContainerManager(dockerClient, agentName, ParseLabels)
	return &Adapter{
		containers:   cm,
		sse:          NewSSEBroadcaster(cm, protocol.AgentVersion, info),
		dockerClient: dockerClient,
		messageSem:   make(chan struct{}, defaultMessageHandlerConcurrency),
	}
}

func (a *Adapter) Name() string { return "drydock" }

func (a *Adapter) Capabilities() []string {
	return []string{
		"dd:watch",
		"dd:trigger",
		"dd:container-sync",
		"dd:logs",
	}
}

func (a *Adapter) HelloExtension() *adapter.HelloExtension {
	return &adapter.HelloExtension{
		DrydockCompat: protocol.DrydockCompat,
		WatcherTypes:  []string{"docker"},
		TriggerTypes:  []string{},
	}
}

func (a *Adapter) PollInterval() int { return 0 }

// OnConnect sends the initial container sync and component sync after an
// edge-mode connection is established.
func (a *Adapter) OnConnect(ctx context.Context, sender adapter.MessageSender) error {
	containers, err := a.containers.BuildInventory(ctx)
	if err != nil {
		slog.Warn("initial container sync failed", "error", err)
	} else {
		a.sendContainerSync(sender, containers)
	}

	a.sendComponentSync(sender)
	return nil
}

// RefreshContainers delegates to the container manager.
func (a *Adapter) RefreshContainers(ctx context.Context) (added, updated, removed []adapter.Container, err error) {
	return a.containers.Refresh(ctx)
}

// OnContainerRefresh sends container events over the edge WebSocket when
// sender is non-nil (edge mode) and always broadcasts over SSE (standard mode).
func (a *Adapter) OnContainerRefresh(ctx context.Context, sender adapter.MessageSender, added, updated, removed []adapter.Container) error {
	// Edge-mode events (sender may be nil in standard mode).
	if sender != nil {
		for _, c := range added {
			a.sendContainerEvent(sender, protocol.TypeDDContainerAdded, c)
		}
		for _, c := range updated {
			a.sendContainerEvent(sender, protocol.TypeDDContainerUpdated, c)
		}
		for _, c := range removed {
			a.sendTypedMessage(sender, protocol.TypeDDContainerRemoved, protocol.DDContainerRemovedMessage{
				ID:   c.ID,
				Name: c.Name,
			})
		}
	}

	// SSE broadcasts (standard mode).
	for _, c := range added {
		a.sse.BroadcastContainerAdded(c)
	}
	for _, c := range updated {
		a.sse.BroadcastContainerUpdated(c)
	}
	for _, c := range removed {
		a.sse.BroadcastContainerRemoved(c.ID, c.Name)
	}

	// Full authoritative snapshot every poll cycle. Drydock relies on this
	// to prune containers that disappeared without a removal event.
	a.sse.BroadcastWatcherSnapshot()
	return nil
}

// HandleMessage handles Drydock-specific WebSocket message types.
func (a *Adapter) HandleMessage(ctx context.Context, sender adapter.MessageSender, msgType string, data json.RawMessage) bool {
	switch msgType {
	case protocol.TypeDDWatchRequest:
		var msg protocol.DDWatchRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("invalid watch_request message", "error", err)
			return true
		}
		a.spawnMessageHandler(ctx, msgType, func() {
			a.handleWatchRequest(sender, msg)
		})
		return true

	case protocol.TypeDDWatchContainerRequest:
		var msg protocol.DDWatchContainerRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("invalid watch_container_request message", "error", err)
			return true
		}
		a.spawnMessageHandler(ctx, msgType, func() {
			a.handleWatchContainerRequest(sender, msg)
		})
		return true

	case protocol.TypeDDTriggerRequest:
		var msg protocol.DDTriggerRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("invalid trigger_request message", "error", err)
			return true
		}
		a.spawnMessageHandler(ctx, msgType, func() {
			a.handleTriggerRequest(sender, msg)
		})
		return true

	case protocol.TypeDDContainerLogRequest:
		var msg protocol.DDContainerLogRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("invalid container_log_request message", "error", err)
			return true
		}
		a.spawnMessageHandler(ctx, msgType, func() {
			a.handleContainerLogRequest(ctx, sender, msg)
		})
		return true

	case protocol.TypeDDContainerDeleteRequest:
		var msg protocol.DDContainerDeleteRequestMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("invalid container_delete_request message", "error", err)
			return true
		}
		a.spawnMessageHandler(ctx, msgType, func() {
			a.handleContainerDeleteRequest(ctx, sender, msg)
		})
		return true
	}

	return false
}

// Containers returns the underlying container manager for use by routes.
func (a *Adapter) Containers() *adapter.ContainerManager { return a.containers }

// SSE returns the SSE broadcaster for use by routes.
func (a *Adapter) SSE() *SSEBroadcaster { return a.sse }

// DockerClient returns the docker client for use by routes.
func (a *Adapter) DockerClient() *docker.Client { return a.dockerClient }

// ParseLabels extracts Drydock-specific labels from a container's label map.
func ParseLabels(labels map[string]string) adapter.LabelResult {
	return adapter.LabelResult{
		DisplayName:   labels[LabelDisplayName],
		DisplayIcon:   labels[LabelDisplayIcon],
		IncludeTags:   labels[LabelTagInclude],
		ExcludeTags:   labels[LabelTagExclude],
		TransformTags: labels[LabelTagTransform],
		Watcher:       labels[LabelWatch],
	}
}

// GetWatcherComponents returns the registered watcher component descriptors.
func GetWatcherComponents() []protocol.ComponentDescriptor {
	return []protocol.ComponentDescriptor{
		{
			Type: "docker",
			Name: "docker",
			Configuration: map[string]any{
				"description":  "Watches Docker containers for updates via Docker Engine API",
				"capabilities": []string{"container-sync", "labels"},
			},
		},
	}
}

// GetTriggerComponents returns the registered trigger component descriptors.
func GetTriggerComponents() []protocol.ComponentDescriptor {
	// No triggers in v1.0 - agent-side triggering deferred to v2.0
	return []protocol.ComponentDescriptor{}
}

// --- internal handlers ---

func (a *Adapter) handleWatchRequest(sender adapter.MessageSender, msg protocol.DDWatchRequestMessage) {
	a.sendTypedMessage(sender, protocol.TypeDDWatchResponse, protocol.DDWatchResponseMessage{
		WatcherType: msg.WatcherType,
		WatcherName: msg.WatcherName,
		Results:     []json.RawMessage{},
	})
}

func (a *Adapter) handleWatchContainerRequest(sender adapter.MessageSender, msg protocol.DDWatchContainerRequestMessage) {
	a.sendTypedMessage(sender, protocol.TypeDDWatchContainerResponse, protocol.DDWatchContainerResponseMessage{
		WatcherType: msg.WatcherType,
		WatcherName: msg.WatcherName,
		ContainerID: msg.ContainerID,
		Result:      nil,
	})
}

func (a *Adapter) handleTriggerRequest(sender adapter.MessageSender, msg protocol.DDTriggerRequestMessage) {
	a.sendTypedMessage(sender, protocol.TypeDDTriggerResponse, protocol.DDTriggerResponseMessage{
		TriggerType: msg.TriggerType,
		TriggerName: msg.TriggerName,
		Success:     false,
		Message:     "triggers not implemented in v1.0",
	})
}

func (a *Adapter) handleContainerLogRequest(ctx context.Context, sender adapter.MessageSender, msg protocol.DDContainerLogRequestMessage) {
	tail := ""
	if msg.Tail > 0 {
		tail = fmt.Sprintf("%d", msg.Tail)
	}

	body, err := a.dockerClient.GetContainerLogs(ctx, msg.ContainerID, tail, msg.Since, msg.Until, false, false)
	if err != nil {
		slog.Warn("failed to get container logs", "container", msg.ContainerID, "error", err)
		a.sendTypedMessage(sender, protocol.TypeDDContainerLogResponse, protocol.DDContainerLogResponseMessage{
			ContainerID: msg.ContainerID,
			Logs:        fmt.Sprintf("error: %v", err),
		})
		return
	}
	defer body.Close()

	data, _ := io.ReadAll(io.LimitReader(body, 100*1024*1024))

	a.sendTypedMessage(sender, protocol.TypeDDContainerLogResponse, protocol.DDContainerLogResponseMessage{
		ContainerID: msg.ContainerID,
		Logs:        string(data),
	})
}

func (a *Adapter) handleContainerDeleteRequest(ctx context.Context, sender adapter.MessageSender, msg protocol.DDContainerDeleteRequestMessage) {
	err := a.dockerClient.RemoveContainer(ctx, msg.ContainerID, true)
	if err != nil {
		slog.Warn("failed to delete container", "container", msg.ContainerID, "error", err)
		a.sendTypedMessage(sender, protocol.TypeDDContainerDeleteResponse, protocol.DDContainerDeleteResponseMessage{
			ContainerID: msg.ContainerID,
			Success:     false,
			Error:       err.Error(),
		})
		return
	}

	a.sendTypedMessage(sender, protocol.TypeDDContainerDeleteResponse, protocol.DDContainerDeleteResponseMessage{
		ContainerID: msg.ContainerID,
		Success:     true,
	})
}

func (a *Adapter) sendContainerSync(sender adapter.MessageSender, containers []adapter.Container) {
	a.sendTypedMessage(sender, protocol.TypeDDContainerSync, struct {
		Containers []adapter.Container `json:"containers"`
	}{
		Containers: containers,
	})
}

func (a *Adapter) sendComponentSync(sender adapter.MessageSender) {
	watchers := GetWatcherComponents()
	triggers := GetTriggerComponents()

	protoWatchers := make([]protocol.ComponentDescriptor, len(watchers))
	for i, w := range watchers {
		protoWatchers[i] = protocol.ComponentDescriptor{
			Type:          w.Type,
			Name:          w.Name,
			Configuration: w.Configuration,
		}
	}
	protoTriggers := make([]protocol.ComponentDescriptor, len(triggers))
	for i, t := range triggers {
		protoTriggers[i] = protocol.ComponentDescriptor{
			Type:          t.Type,
			Name:          t.Name,
			Configuration: t.Configuration,
		}
	}

	a.sendTypedMessage(sender, protocol.TypeDDComponentSync, protocol.DDComponentSyncMessage{
		Watchers: protoWatchers,
		Triggers: protoTriggers,
	})
}

func (a *Adapter) sendContainerEvent(sender adapter.MessageSender, msgType string, container adapter.Container) {
	data, err := json.Marshal(container)
	if err != nil {
		slog.Warn("failed to marshal container event", "id", container.ID, "error", err)
		return
	}

	switch msgType {
	case protocol.TypeDDContainerAdded:
		a.sendTypedMessage(sender, msgType, protocol.DDContainerAddedMessage{
			Container: json.RawMessage(data),
		})
	case protocol.TypeDDContainerUpdated:
		a.sendTypedMessage(sender, msgType, protocol.DDContainerUpdatedMessage{
			Container: json.RawMessage(data),
		})
	}
}

func (a *Adapter) sendTypedMessage(sender adapter.MessageSender, msgType string, data any) {
	if sender == nil {
		slog.Warn("failed to send typed message: sender is nil", "type", msgType)
		return
	}

	if err := sender.SendTypedMessage(msgType, data); err != nil {
		slog.Warn("failed to send typed message", "type", msgType, "error", err)
	}
}

func (a *Adapter) spawnMessageHandler(ctx context.Context, msgType string, fn func()) {
	sem := a.getMessageSemaphore()

	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		slog.Debug("skipping message handler due to canceled context", "type", msgType, "error", ctx.Err())
		return
	}

	go func() {
		defer func() { <-sem }()

		if ctx.Err() != nil {
			return
		}

		fn()
	}()
}

func (a *Adapter) getMessageSemaphore() chan struct{} {
	a.semInit.Do(func() {
		if a.messageSem == nil {
			a.messageSem = make(chan struct{}, defaultMessageHandlerConcurrency)
		}
	})
	return a.messageSem
}
