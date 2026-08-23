package protocol

// HealthResponse is the wire contract shared by standard mode's /health,
// /ready, and /_portwing/health handlers and edge mode's operations
// listener of the same name. It mirrors the HealthResponse schema in
// api/openapi.yaml — keep the two in sync when either changes.
type HealthResponse struct {
	Status        string  `json:"status"`
	Live          bool    `json:"live"`
	Ready         bool    `json:"ready"`
	Mode          string  `json:"mode"`
	Version       string  `json:"version"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
	Docker        string  `json:"docker"`
	Controller    string  `json:"controller"`
}
