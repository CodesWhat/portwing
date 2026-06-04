package protocol

import "encoding/json"

// Core message types.
const (
	TypeHello          = "hello"
	TypeWelcome        = "welcome"
	TypeRequest        = "request"
	TypeResponse       = "response"
	TypeStream         = "stream"
	TypeStreamEnd      = "stream_end"
	TypeMetrics        = "metrics"
	TypeContainerEvent = "container_event"
	TypePing           = "ping"
	TypePong           = "pong"
	TypeError          = "error"
	TypeExecStart      = "exec_start"
	TypeExecReady      = "exec_ready"
	TypeExecInput      = "exec_input"
	TypeExecOutput     = "exec_output"
	TypeExecResize     = "exec_resize"
	TypeExecEnd        = "exec_end"
)

// Drydock-specific message types.
const (
	TypeDDContainerSync          = "dd:container_sync"
	TypeDDContainerAdded         = "dd:container_added"
	TypeDDContainerUpdated       = "dd:container_updated"
	TypeDDContainerRemoved       = "dd:container_removed"
	TypeDDComponentSync          = "dd:component_sync"
	TypeDDWatchRequest           = "dd:watch_request"
	TypeDDWatchResponse          = "dd:watch_response"
	TypeDDWatchContainerRequest  = "dd:watch_container_request"
	TypeDDWatchContainerResponse = "dd:watch_container_response"
	TypeDDTriggerRequest         = "dd:trigger_request"
	TypeDDTriggerResponse        = "dd:trigger_response"
	TypeDDContainerLogRequest    = "dd:container_log_request"
	TypeDDContainerLogResponse   = "dd:container_log_response"
)

type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type HelloMessage struct {
	Version       string   `json:"version"`
	Protocol      string   `json:"protocol"`
	AgentID       string   `json:"agentId"`
	AgentName     string   `json:"agentName"`
	TokenHash     string   `json:"tokenHash"`
	DockerVersion string   `json:"dockerVersion"`
	Hostname      string   `json:"hostname"`
	Capabilities  []string `json:"capabilities"`
	DrydockCompat string   `json:"drydockCompat"`
	WatcherTypes  []string `json:"watcherTypes"`
	TriggerTypes  []string `json:"triggerTypes"`
}

type WelcomeMessage struct {
	PollInterval int               `json:"pollInterval"`
	Config       map[string]string `json:"config,omitempty"`
}

type RequestMessage struct {
	RequestID string            `json:"requestId"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`
}

type ResponseMessage struct {
	RequestID   string            `json:"requestId"`
	StatusCode  int               `json:"statusCode"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	IsStream    bool              `json:"isStream,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
}

type StreamMessage struct {
	RequestID string `json:"requestId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Data      string `json:"data"` // base64
}

type StreamEndMessage struct {
	RequestID string `json:"requestId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type ExecStartMessage struct {
	ExecID      string   `json:"execId"`
	ContainerID string   `json:"containerId"`
	Cmd         []string `json:"cmd"`
	User        string   `json:"user,omitempty"`
	Cols        int      `json:"cols"`
	Rows        int      `json:"rows"`
}

type ExecReadyMessage struct {
	ExecID string `json:"execId"`
}

type ExecInputMessage struct {
	ExecID string `json:"execId"`
	Data   string `json:"data"` // base64
}

type ExecOutputMessage struct {
	ExecID string `json:"execId"`
	Data   string `json:"data"` // base64
}

type ExecResizeMessage struct {
	ExecID string `json:"execId"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

type ExecEndMessage struct {
	ExecID string `json:"execId"`
	Reason string `json:"reason,omitempty"`
}

type MetricsMessage struct {
	CPUUsage       float64 `json:"cpuUsage"`
	CPUCores       int     `json:"cpuCores"`
	MemoryTotal    uint64  `json:"memoryTotal"`
	MemoryUsed     uint64  `json:"memoryUsed"`
	MemoryFree     uint64  `json:"memoryFree"`
	DiskTotal      uint64  `json:"diskTotal"`
	DiskUsed       uint64  `json:"diskUsed"`
	DiskFree       uint64  `json:"diskFree"`
	NetworkRxBytes uint64  `json:"networkRxBytes"`
	NetworkTxBytes uint64  `json:"networkTxBytes"`
	Uptime         uint64  `json:"uptime"`
}

type ContainerEventMessage struct {
	ContainerID     string            `json:"containerId"`
	ContainerName   string            `json:"containerName"`
	Image           string            `json:"image"`
	Action          string            `json:"action"`
	ActorAttributes map[string]string `json:"actorAttributes,omitempty"`
	Timestamp       string            `json:"timestamp"`
}

type ErrorMessage struct {
	Message   string `json:"message"`
	Code      string `json:"code,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type PingMessage struct {
	Timestamp int64 `json:"timestamp"`
}

type PongMessage struct {
	Timestamp int64 `json:"timestamp"`
}

// Drydock-specific messages.
// Container and ContainerResult types are defined in internal/adapter/model.go.
// We use json.RawMessage here to avoid circular dependencies.

type DDContainerSyncMessage struct {
	Containers []json.RawMessage `json:"containers"`
}

type DDContainerAddedMessage struct {
	Container json.RawMessage `json:"container"`
}

type DDContainerUpdatedMessage struct {
	Container json.RawMessage `json:"container"`
}

type DDContainerRemovedMessage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type DDComponentSyncMessage struct {
	Watchers []ComponentDescriptor `json:"watchers"`
	Triggers []ComponentDescriptor `json:"triggers"`
}

type ComponentDescriptor struct {
	Type          string                 `json:"type"`
	Name          string                 `json:"name"`
	Configuration map[string]interface{} `json:"configuration,omitempty"`
}

type DDWatchRequestMessage struct {
	WatcherType string `json:"watcherType"`
	WatcherName string `json:"watcherName"`
}

type DDWatchResponseMessage struct {
	WatcherType string            `json:"watcherType"`
	WatcherName string            `json:"watcherName"`
	Results     []json.RawMessage `json:"results"` // ContainerResult; see internal/adapter/model.go
}

type DDWatchContainerRequestMessage struct {
	WatcherType string `json:"watcherType"`
	WatcherName string `json:"watcherName"`
	ContainerID string `json:"containerId"`
}

type DDWatchContainerResponseMessage struct {
	WatcherType string           `json:"watcherType"`
	WatcherName string           `json:"watcherName"`
	ContainerID string           `json:"containerId"`
	Result      *json.RawMessage `json:"result,omitempty"` // ContainerResult; see internal/adapter/model.go
}

type DDTriggerRequestMessage struct {
	TriggerType string   `json:"triggerType"`
	TriggerName string   `json:"triggerName"`
	ContainerID string   `json:"containerId,omitempty"`
	Containers  []string `json:"containers,omitempty"`
	Batch       bool     `json:"batch,omitempty"`
}

type DDTriggerResponseMessage struct {
	TriggerType string `json:"triggerType"`
	TriggerName string `json:"triggerName"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
}

type DDContainerLogRequestMessage struct {
	ContainerID string `json:"containerId"`
	Tail        int    `json:"tail,omitempty"`
	Since       string `json:"since,omitempty"`
	Until       string `json:"until,omitempty"`
	Follow      bool   `json:"follow,omitempty"`
}

type DDContainerLogResponseMessage struct {
	ContainerID string `json:"containerId"`
	Logs        string `json:"logs"`
}
