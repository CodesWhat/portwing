package edge

// Terminal vs. transient classification for drydock controller hello
// rejections. The controller answers a rejected hello with an `error` envelope
// carrying a string Code, then closes the socket. Terminal codes wrap errFatal
// in connect() so Run() exits with an actionable error instead of reconnecting
// forever; every other code — including unrecognized ones — keeps retrying with
// backoff.
//
// This vocabulary is NOT part of portwing's documented SPEC/COMPATIBILITY wire
// contract. It is sourced by reading the drydock controller directly (drydock
// app/api/portwing-ws.ts, commit 06e5570b, 2026-07-04), so it can drift if
// drydock renames or repurposes a code. Keep it in lockstep when the drydock
// handshake changes. Unrecognized codes deliberately default to retry (see
// connect): that degrades to pre-classifier behavior for a genuinely-new
// terminal code (no regression), whereas defaulting to terminal would
// crash-loop the whole fleet the moment drydock introduces a new *transient*
// code (maintenance, capacity, a new rate-limit tier).

// terminalHelloRejectCodes are rejections that retrying with the SAME agent
// configuration cannot clear — they need an operator or config change (a new or
// re-enrolled key, a corrected AGENT_NAME, a protocol/agent upgrade, or a
// non-conflicting name). Backoff-and-retry only burns cycles for these.
var terminalHelloRejectCodes = map[string]bool{
	"ed25519-required":   true, // controller requires a signed hello; agent sent token-only
	"unknown-key":        true, // pubKeyId not in the registry (revoked or never enrolled)
	"bad-signature":      true, // Ed25519 verification failed / signature malformed
	"protocol-mismatch":  true, // hello.protocol != the controller's expected version
	"no-auth":            true, // neither Ed25519 nor token credentials present
	"invalid-agent-name": true, // agentName wrong type or too long
	"parse-error":        true, // malformed hello envelope / missing required fields
	"expected-hello":     true, // first frame was not a hello (client bug)
	"agent-name-claimed": true, // name already bound to a different pubKeyId
}

// transientHelloRejectCodes are rejections tied to server-side or timing state
// that a later attempt is expected to clear without operator action. Every
// hello is re-signed with a fresh timestamp and nonce, so skew/nonce/replay
// rejections should not recur; capacity and connection-race limits self-heal.
var transientHelloRejectCodes = map[string]bool{
	"timestamp-skew":          true, // |now - hello.timestamp| outside the allowed window
	"bad-nonce":               true, // nonce failed format check (fresh nonce next attempt)
	"replay":                  true, // nonce already seen / replay-cache pressure
	"internal-error":          true, // controller-side error while verifying the hello
	"rate-limited":            true, // per-key admission window exceeded
	"registry-full":           true, // name-binding map full (prunes over time)
	"agent-already-connected": true, // prior session not yet torn down (reconnect race)
}

// isTerminalHelloRejection reports whether a hello-rejection Code is fatal, i.e.
// reconnecting with the same configuration cannot succeed.
func isTerminalHelloRejection(code string) bool {
	return terminalHelloRejectCodes[code]
}

// isKnownHelloRejection reports whether a hello-rejection Code is one portwing
// classifies explicitly. Unknown codes default to the retry path (see connect)
// but are logged distinctly so a permanent-but-unrecognized code stays visible.
func isKnownHelloRejection(code string) bool {
	return terminalHelloRejectCodes[code] || transientHelloRejectCodes[code]
}
