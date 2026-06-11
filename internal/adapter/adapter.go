package adapter

import (
	"context"
	"encoding/json"
	"net/http"
)

// MessageSender allows adapters to send typed messages over the active
// transport (WebSocket in edge mode, SSE in standard mode, etc.).
type MessageSender interface {
	SendTypedMessage(msgType string, data interface{}) error
}

// LabelParser extracts adapter-specific metadata from container labels.
type LabelParser func(labels map[string]string) LabelResult

// LabelResult holds the parsed label values used by the container manager.
type LabelResult struct {
	DisplayName   string
	DisplayIcon   string
	IncludeTags   string
	ExcludeTags   string
	TransformTags string
	Watcher       string
}

// HelloExtension carries adapter-specific fields merged into the hello
// handshake message during edge-mode connection.
type HelloExtension struct {
	DrydockCompat string   `json:"drydockCompat,omitempty"`
	WatcherTypes  []string `json:"watcherTypes,omitempty"`
	TriggerTypes  []string `json:"triggerTypes,omitempty"`
}

// MetadataAdapter exposes adapter identity and capability metadata.
type MetadataAdapter interface {
	// Name returns a short identifier for the adapter (e.g. "drydock", "generic").
	Name() string

	// Capabilities returns the list of capability strings advertised by this
	// adapter (merged into the info endpoint and hello message).
	Capabilities() []string

	// HelloExtension returns adapter-specific fields for the hello handshake.
	// Return nil if the adapter has nothing to add.
	HelloExtension() *HelloExtension
}

// ContainerSyncAdapter exposes container inventory sync behavior.
type ContainerSyncAdapter interface {
	// OnConnect is called after a successful edge-mode connection (hello/welcome
	// handshake complete). The adapter can send initial sync messages here.
	OnConnect(ctx context.Context, sender MessageSender) error

	// RefreshContainers rebuilds the container inventory and returns the diff.
	RefreshContainers(ctx context.Context) (added, updated, removed []Container, err error)

	// OnContainerRefresh is called after RefreshContainers with the diff.
	// In edge mode, sender is non-nil and can be used for outbound typed
	// messages. In standard/server mode, sender is nil and adapters should use
	// in-process transports (for example SSE broadcasters) instead.
	OnContainerRefresh(ctx context.Context, sender MessageSender, added, updated, removed []Container) error

	// PollInterval returns the container poll interval in seconds.
	// Return 0 to use the default from config.
	PollInterval() int
}

// MessageHandlingAdapter handles adapter-specific incoming messages.
type MessageHandlingAdapter interface {
	// HandleMessage is called for message types not handled by the core.
	// Return true if the adapter handled the message, false to ignore it.
	HandleMessage(ctx context.Context, sender MessageSender, msgType string, data json.RawMessage) bool
}

// RouteAdapter registers adapter-specific HTTP routes.
type RouteAdapter interface {
	// RegisterRoutes registers adapter-specific HTTP routes on the given mux.
	RegisterRoutes(mux *http.ServeMux, authMiddleware func(http.HandlerFunc) http.Handler)
}

// EdgeAdapter is the adapter contract required by edge mode.
type EdgeAdapter interface {
	MetadataAdapter
	ContainerSyncAdapter
	MessageHandlingAdapter
}

// ServerAdapter is the adapter contract required by standard/server mode.
type ServerAdapter interface {
	MetadataAdapter
	ContainerSyncAdapter
	RouteAdapter
}

// Adapter is the complete adapter contract implemented by concrete adapters.
type Adapter interface {
	EdgeAdapter
	ServerAdapter
}
