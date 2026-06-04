package adapter

type ContainerUpdateKind string

const (
	UpdateKindUnknown ContainerUpdateKind = "unknown"
	UpdateKindMajor   ContainerUpdateKind = "major"
	UpdateKindMinor   ContainerUpdateKind = "minor"
	UpdateKindPatch   ContainerUpdateKind = "patch"
	UpdateKindDigest  ContainerUpdateKind = "digest"
)

type Container struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	DisplayIcon string            `json:"displayIcon,omitempty"`
	Status      string            `json:"status"`
	Watcher     string            `json:"watcher"`
	Agent       string            `json:"agent,omitempty"`

	Image  ContainerImage   `json:"image"`
	Result *ContainerResult `json:"result,omitempty"`
	Error  *ContainerError  `json:"error,omitempty"`

	UpdateAvailable bool                `json:"updateAvailable"`
	UpdateKind      ContainerUpdateKind `json:"updateKind"`

	IncludeTags   string `json:"includeTags,omitempty"`
	ExcludeTags   string `json:"excludeTags,omitempty"`
	TransformTags string `json:"transformTags,omitempty"`

	Labels  map[string]string `json:"labels,omitempty"`
	Details *RuntimeDetails   `json:"details,omitempty"`
}

type ContainerImage struct {
	ID           string `json:"id"`
	Registry     string `json:"registry"`
	Name         string `json:"name"`
	Tag          string `json:"tag"`
	Digest       string `json:"digest,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	OS           string `json:"os,omitempty"`
	Created      string `json:"created,omitempty"`
	Size         int64  `json:"size,omitempty"`
}

type ContainerResult struct {
	Tag     string `json:"tag,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Created string `json:"created,omitempty"`
	Link    string `json:"link,omitempty"`
}

type ContainerError struct {
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

type RuntimeDetails struct {
	Platform string        `json:"platform,omitempty"`
	Command  string        `json:"command,omitempty"`
	Ports    []PortMapping `json:"ports,omitempty"`
	Network  []NetworkInfo `json:"network,omitempty"`
	Volumes  []VolumeInfo  `json:"volumes,omitempty"`
	Created  string        `json:"created,omitempty"`
	Started  string        `json:"started,omitempty"`
	Health   string        `json:"health,omitempty"`
}

type PortMapping struct {
	Container uint16 `json:"container"`
	Host      uint16 `json:"host,omitempty"`
	Protocol  string `json:"protocol"`
	IP        string `json:"ip,omitempty"`
}

type NetworkInfo struct {
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
}

type VolumeInfo struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
}
