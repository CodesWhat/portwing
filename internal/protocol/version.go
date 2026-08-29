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

	// CapRequestBodyStream is the capability token, exact and byte-for-byte
	// identical on both the controller and agent sides, that gates the
	// request.bodyStream wire field (see RequestMessage in messages.go).
	//
	// This is a capability negotiated via HelloMessage.Capabilities, not a
	// ProtocolVersion/ProtocolString bump: a version bump is a terminal,
	// hard protocol-mismatch that permanently breaks any pairing of
	// mismatched agent/controller versions, whereas an unrecognized
	// capability token degrades gracefully to legacy behavior. Unlike
	// CapResponseBodyBase64 (negotiated agent-side via welcome.capabilities,
	// since the agent is the one producing the response), this one is
	// load-bearing in the agent's own hello: it tells the controller the
	// agent can reassemble a request body delivered as follow-up
	// stream/stream_end frames instead of inline in request.body, so a
	// controller must not send request.bodyStream=true unless the agent's
	// hello advertised this token.
	CapRequestBodyStream = "edge-request-body-stream"
)

// AgentVersion is the agent build version. It must be a var, not a const:
// releases override it via
// -ldflags "-X github.com/codeswhat/portwing/internal/protocol.AgentVersion=...".
var AgentVersion = "0.0.0-dev"
