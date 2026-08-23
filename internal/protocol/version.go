package protocol

const (
	ProtocolName    = "portwing"
	ProtocolVersion = "1.0"
	ProtocolString  = "portwing/1.0"

	// DrydockCompat is the serverCompatLevel the agent expects in the welcome
	// frame. Both sides compare major-version only, so minor/patch bumps on
	// either end do not produce warnings. Increment the major component when
	// introducing a breaking wire-protocol change.
	DrydockCompat = "1.4.0"

	// CapResponseBodyBase64 is the capability token, exact and byte-for-byte
	// identical on both the controller and agent sides, that gates the
	// response.bodyBase64 wire field (see ResponseMessage in messages.go).
	//
	// This is a capability negotiated via WelcomeMessage.Capabilities, not a
	// ProtocolVersion/ProtocolString bump: a version bump is a terminal,
	// hard protocol-mismatch that permanently breaks any pairing of
	// mismatched agent/controller versions, whereas an unrecognized
	// capability token degrades gracefully to legacy behavior. An older
	// controller simply omits this token from its welcome, so a newer agent
	// falls back to the pre-existing (non-base64) response body path.
	CapResponseBodyBase64 = "edge-response-body-b64"
)

// AgentVersion is the agent build version. It must be a var, not a const:
// releases override it via
// -ldflags "-X github.com/codeswhat/portwing/internal/protocol.AgentVersion=...".
var AgentVersion = "0.0.0-dev"
