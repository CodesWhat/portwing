package protocol

const (
	ProtocolName    = "portwing"
	ProtocolVersion = "1.0"
	ProtocolString  = "portwing/1.0"
	DrydockCompat   = "1.4.0"
)

// AgentVersion is the agent build version. It must be a var, not a const:
// releases override it via
// -ldflags "-X github.com/codeswhat/portwing/internal/protocol.AgentVersion=...".
var AgentVersion = "0.0.0-dev"
