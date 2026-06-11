package protocol

const (
	ProtocolName    = "lookout"
	ProtocolVersion = "1.0"
	ProtocolString  = "lookout/1.0"
	DrydockCompat   = "1.4.0"
)

// AgentVersion is the agent build version. It must be a var, not a const:
// releases override it via
// -ldflags "-X github.com/codeswhat/lookout/internal/protocol.AgentVersion=...".
var AgentVersion = "0.1.0"
