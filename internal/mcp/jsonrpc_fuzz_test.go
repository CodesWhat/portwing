package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// FuzzMCPHandler feeds arbitrary JSON bodies to the MCP HTTP handler and verifies:
//   - The handler never panics.
//   - The response is always valid JSON (when it has a body).
//   - The response JSONRPC field is always "2.0" on success paths.
func FuzzMCPHandler(f *testing.F) {
	// Seed: valid MCP JSON-RPC requests.
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	f.Add(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	f.Add(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","id":1.5,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	f.Add(`{"jsonrpc":"2.0","id":"request-1","error":{"code":-32601,"message":"not found"}}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"result":null}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"result":[]}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"result":"value"}`)
	f.Add(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"list_containers","arguments":{}}}`)
	f.Add(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"inspect_container","arguments":{"id":"abc"}}}`)
	f.Add(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"container_logs","arguments":{"id":"abc","tail":100}}}`)
	f.Add(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"container_stats","arguments":{"id":"abc"}}}`)
	f.Add(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"host_metrics","arguments":{}}}`)
	// Seed: hostile inputs.
	f.Add(``)
	f.Add(`{}`)
	f.Add(`{"jsonrpc":"1.0","id":1,"method":"initialize"}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":""}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"unknown/method"}`)
	f.Add(`not json at all`)
	f.Add(`{"jsonrpc":"2.0","id":` + strings.Repeat("1", 100) + `,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","id":null,"method":"tools/call","params":null}`)
	f.Add(`{"jsonrpc":"2.0","id":true,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","id":{},"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","id":[],"method":"ping"}`)
	f.Add(`{"jsonrpc":"1.0","id":{},"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0"}`)
	f.Add(`{"jsonrpc":"2.0","method":null}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"ping","params":true}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + strings.Repeat("x", 10000) + `"}}`)
	f.Add(`{"0000000":"000","id":"&"}`)
	f.Add(`{"jsonrpc":"2.0","id":-1.5e3,"method":"ping"}`)
	f.Add(`{"jsonrpc":"2.0","id":"a","method":1}`)
	f.Add(`{"jsonrpc":"2.0","id":"a","method":{}}`)
	f.Add(`{"jsonrpc":"2.0","id": "a" ,"method": "ping" }`)
	// Seed: 2026-07-28 requests. modernFuzzErrors derives the headers.
	f.Add(fuzzModernSeed("server/discover", `"2026-07-28"`, `{}`, ``))
	f.Add(fuzzModernSeed("tools/list", `"2026-07-28"`, `{"roots":{}}`, ``))
	f.Add(fuzzModernSeed("tools/call", `"2026-07-28"`, `{}`, `,"name":"list_containers","arguments":{}`))
	f.Add(fuzzModernSeed("tools/call", `"2026-07-28"`, `{}`, `,"name":"héllo"`))
	f.Add(fuzzModernSeed("tools/call", `"2026-07-28"`, `{}`, `,"name":"=?base64?x?="`))
	f.Add(fuzzModernSeed("tools/call", `"2026-07-28"`, `{}`, `,"name":7`))
	f.Add(fuzzModernSeed("resources/read", `"2026-07-28"`, `{}`, `,"uri":"file:///x"`))
	f.Add(fuzzModernSeed("ping", `"2026-07-28"`, `{}`, ``))
	f.Add(fuzzModernSeed("initialize", `"2026-07-28"`, `{}`, `,"protocolVersion":"2025-11-25"`))
	f.Add(fuzzModernSeed("tools/list", `"2099-01-01"`, `{}`, ``))
	f.Add(fuzzModernSeed("tools/list", `"2025-11-25"`, `{}`, ``))
	f.Add(fuzzModernSeed("tools/list", `20260728`, `{}`, ``))
	f.Add(fuzzModernSeed("tools/list", `"2026-07-28"`, `[]`, ``))
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	f.Add(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"host_metrics","_meta":{"progressToken":1}}}`)
	f.Add(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)

	// Use a nil docker client and nil collector; the handler must not reach
	// them for the JSON-RPC error paths exercised by fuzz inputs. For the
	// tools/call path that does need docker, a nil docker client will return
	// an error from the tool — which is a valid non-panic outcome.
	h := &Handler{docker: nil, collector: nil}

	f.Fuzz(func(t *testing.T, body string) {
		// Protocol negotiation: replay the body with 2026-07-28 and
		// 2025-11-25 headers and check each outcome against an independent
		// model of the revision's rules.
		for _, err := range modernFuzzErrors(h, body) {
			t.Error(err)
		}

		// Parse into raw fields independently of the production request
		// decoder so malformed envelopes cannot be mistaken for notifications.
		var requestFields map[string]json.RawMessage
		requestObject := json.Unmarshal([]byte(body), &requestFields) == nil && requestFields != nil
		requestID, hadID := requestFields["id"]
		// The raw id and method are valid JSON here (the parent Unmarshal
		// succeeded), so the byte-prefix checks must match their decoder-based
		// references.
		for _, key := range []string{"id", "method"} {
			if raw, ok := requestFields[key]; ok {
				if err := envelopeParityError(raw); err != nil {
					t.Error(err)
				}
			}
		}
		requestIDValue, requestIDErr := decodeJSONValue(requestID)
		version, versionOK := decodeJSONValue(requestFields["jsonrpc"])
		_, hasMethod := requestFields["method"]
		method, methodOK := decodeJSONValue(requestFields["method"])
		_, methodIsString := method.(string)
		params, hasParams := requestFields["params"]
		paramsValid := !hasParams || isFuzzParams(params)
		result, hasResult := requestFields["result"]
		rpcError, hasError := requestFields["error"]
		isResponseMessage := requestObject && !hasMethod && (hasResult || hasError)
		validResponseMessage := isResponseMessage && versionOK == nil && version == "2.0" &&
			hadID && requestIDErr == nil && isFuzzRequestID(requestIDValue) && hasResult != hasError &&
			((hasResult && isFuzzParams(result)) || (hasError && isFuzzRPCError(rpcError)))
		isNotificationMessage := requestObject && versionOK == nil && version == "2.0" &&
			methodOK == nil && methodIsString && !hadID && !hasResult && !hasError
		expectsNoBody := isNotificationMessage || isResponseMessage

		req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		// Must never panic.
		h.ServeHTTP(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		// Status must be a valid HTTP status.
		if resp.StatusCode < 100 || resp.StatusCode > 599 {
			t.Errorf("unexpected status %d", resp.StatusCode)
		}
		if isNotificationMessage && paramsValid && resp.StatusCode != http.StatusAccepted {
			t.Errorf("valid notification status = %d, want %d", resp.StatusCode, http.StatusAccepted)
		}
		if isNotificationMessage && !paramsValid && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid notification status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
		if isResponseMessage && validResponseMessage && resp.StatusCode != http.StatusAccepted {
			t.Errorf("valid response message status = %d, want %d", resp.StatusCode, http.StatusAccepted)
		}
		if isResponseMessage && !validResponseMessage && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("invalid response message status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}

		// If there's a body, it must be parseable JSON.
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Errorf("reading response body: %v", err)
			return
		}

		if len(b) == 0 {
			if !expectsNoBody {
				t.Error("response body is empty for a request")
			}
			return
		}
		if expectsNoBody {
			t.Errorf("notification or response message received a response body: %s", b)
			return
		}

		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(b, &envelope); err != nil {
			t.Errorf("response body is not valid JSON: %v\nbody: %s", err, b)
			return
		}

		// If there is a jsonrpc field, it must be "2.0".
		if raw, ok := envelope["jsonrpc"]; ok {
			var ver string
			if err := json.Unmarshal(raw, &ver); err != nil || ver != "2.0" {
				t.Errorf("jsonrpc field is not \"2.0\": %s", raw)
			}
		}

		// A response must correlate to its request. Valid string and numeric
		// IDs are compared by value; invalid MCP IDs receive a null ID.
		// Decode with UseNumber so equivalent string escapes compare by value
		// while out-of-float64-range numbers retain their exact representation.
		if hadID {
			respID, ok := envelope["id"]
			if !ok {
				t.Errorf("response missing id field for request with id %s", requestID)
				return
			}
			if requestIDErr != nil {
				t.Errorf("decoding request id %s: %v", requestID, requestIDErr)
				return
			}
			responseID, err := decodeJSONValue(respID)
			if err != nil {
				t.Errorf("decoding response id %s: %v", respID, err)
				return
			}
			if !isFuzzRequestID(requestIDValue) {
				if responseID != nil {
					t.Errorf("response id %s is not null for invalid request id %s", respID, requestID)
				}
			} else if !reflect.DeepEqual(requestIDValue, responseID) {
				t.Errorf("response id %s does not match request id %s", respID, requestID)
			}
		}
	})
}

func isFuzzRequestID(value any) bool {
	switch value.(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

func isFuzzParams(raw json.RawMessage) bool {
	var params map[string]json.RawMessage
	return json.Unmarshal(raw, &params) == nil && params != nil
}

func isFuzzRPCError(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	code, err := decodeJSONValue(fields["code"])
	if err != nil {
		return false
	}
	number, ok := code.(json.Number)
	if !ok {
		return false
	}
	if _, err := number.Int64(); err != nil {
		return false
	}
	message, err := decodeJSONValue(fields["message"])
	if err != nil {
		return false
	}
	_, ok = message.(string)
	return ok
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

// The reference helpers live in this file because the ClusterFuzzLite build
// compiles each fuzz file on its own, without the package's other test files.

// referenceValidRequestID is the decoder-based implementation that
// validRequestID replaced. It stays here as the oracle for the differential
// checks. It agrees with validRequestID on every valid JSON value, which is
// all the handler ever passes (values come out of a parent Unmarshal).
func referenceValidRequestID(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var id any
	if err := decoder.Decode(&id); err != nil {
		return false
	}
	switch id.(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

// referenceMethodName is the any-based method decode that the string-prefix
// check plus direct Unmarshal replaced.
func referenceMethodName(raw json.RawMessage) (string, bool) {
	var method any
	_ = json.Unmarshal(raw, &method)
	name, ok := method.(string)
	return name, ok
}

// methodName adapts the production decodeMethod to the reference's shape.
func methodName(raw json.RawMessage) (string, bool) {
	var name string
	if !decodeMethod(raw, &name) {
		return "", false
	}
	return name, true
}

// envelopeParityError reports a disagreement between the production checks
// and their references on raw. Callers must pass valid JSON. It takes no
// testing type so the fuzz body and the table test can both call it.
func envelopeParityError(raw json.RawMessage) error {
	if got, want := validRequestID(raw), referenceValidRequestID(raw); got != want {
		return fmt.Errorf("validRequestID(%q) = %v, reference = %v", raw, got, want)
	}
	gotName, gotOK := methodName(raw)
	wantName, wantOK := referenceMethodName(raw)
	if gotOK != wantOK || gotName != wantName {
		return fmt.Errorf("method(%q) = (%q, %v), reference = (%q, %v)", raw, gotName, gotOK, wantName, wantOK)
	}
	return nil
}

// The 2026-07-28 helpers below follow the same rule as the references above:
// they live in this file and take no testing type.

const (
	fuzzModernVersion  = "2026-07-28"
	fuzzVersionKey     = "io.modelcontextprotocol/protocolVersion"
	fuzzCapabilityKey  = "io.modelcontextprotocol/clientCapabilities"
	fuzzBase64Sentinel = "=?base64?"
)

// fuzzModernSeed builds a request whose _meta names version and capabilities
// (both raw JSON). extraParams is spliced into params after _meta.
func fuzzModernSeed(method, version, capabilities, extraParams string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{"_meta":{"` +
		fuzzVersionKey + `":` + version + `,"` + fuzzCapabilityKey + `":` + capabilities + `}` + extraParams + `}}`
}

// fuzzRequest is the fuzz oracle's own reading of a request body.
type fuzzRequest struct {
	// reaches is true when the body passes every JSON-RPC envelope check and
	// carries an id, so the handler hands it to negotiate.
	reaches bool
	id      any
	method  string
	params  map[string]json.RawMessage
	claimed bool // params._meta holds the protocol version key
	version any  // that key's decoded value
	capsOK  bool // the client capabilities key holds an object
}

func parseFuzzRequest(body string) fuzzRequest {
	var req fuzzRequest
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &fields) != nil || fields == nil {
		return req
	}
	rawID, hasID := fields["id"]
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	id, idErr := decodeJSONValue(rawID)
	version, versionErr := decodeJSONValue(fields["jsonrpc"])
	method, methodErr := decodeJSONValue(fields["method"])
	name, methodIsString := method.(string)
	if !hasID || idErr != nil || !isFuzzRequestID(id) || versionErr != nil || version != "2.0" ||
		hasResult || hasError || methodErr != nil || !methodIsString {
		return req
	}
	if rawParams, ok := fields["params"]; ok {
		if json.Unmarshal(rawParams, &req.params) != nil || req.params == nil {
			return req
		}
	}
	req.reaches, req.id, req.method = true, id, name

	var meta map[string]json.RawMessage
	if rawMeta, ok := req.params["_meta"]; ok && json.Unmarshal(rawMeta, &meta) == nil {
		var rawVersion json.RawMessage
		rawVersion, req.claimed = meta[fuzzVersionKey]
		req.version, _ = decodeJSONValue(rawVersion)
		caps, _ := decodeJSONValue(meta[fuzzCapabilityKey])
		_, req.capsOK = caps.(map[string]any)
	}
	return req
}

// fuzzTools is the set of tools the server implements.
var fuzzTools = map[string]bool{
	"list_containers": true, "inspect_container": true, "container_logs": true, "host_metrics": true, "container_stats": true,
}

// fuzzNameField is the revision's table of params fields Mcp-Name mirrors.
func fuzzNameField(method string) string {
	return map[string]string{"tools/call": "name", "prompts/get": "name", "resources/read": "uri"}[method]
}

// fuzzHeaderValue encodes v for a header the way the revision tells clients
// to: plain when it is visible ASCII, space or tab and doesn't look like the
// sentinel, Base64 otherwise.
func fuzzHeaderValue(v string) string {
	plain := !strings.HasPrefix(v, fuzzBase64Sentinel) || !strings.HasSuffix(v, "?=")
	for _, c := range []byte(v) {
		if c != '\t' && (c < 0x20 || c > 0x7e) {
			plain = false
		}
	}
	if plain {
		return v
	}
	return fuzzBase64Sentinel + base64.StdEncoding.EncodeToString([]byte(v)) + "?="
}

// consistentHeaders returns the headers a conforming 2026-07-28 client would
// send with req, given the protocol version it claims.
func (req fuzzRequest) consistentHeaders(version string) map[string]string {
	headers := map[string]string{"Mcp-Protocol-Version": version, "Mcp-Method": req.method}
	if field := fuzzNameField(req.method); field != "" {
		value, _ := decodeJSONValue(req.params[field])
		if name, ok := value.(string); ok {
			headers["Mcp-Name"] = fuzzHeaderValue(name)
		}
	}
	return headers
}

type fuzzReply struct {
	status   int
	body     []byte
	envelope map[string]any
}

func fuzzPost(h *Handler, body string, headers map[string]string) fuzzReply {
	req := httptest.NewRequest(http.MethodPost, "/_portwing/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header[http.CanonicalHeaderKey(k)] = []string{v}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	reply := fuzzReply{status: w.Code, body: w.Body.Bytes()}
	if value, err := decodeJSONValue(reply.body); err == nil {
		reply.envelope, _ = value.(map[string]any)
	}
	return reply
}

// errorCode returns the JSON-RPC error code, or 0 when there is none.
func (r fuzzReply) errorCode() int64 {
	rpcErr, _ := r.envelope["error"].(map[string]any)
	code, _ := rpcErr["code"].(json.Number)
	n, _ := code.Int64()
	return n
}

// expectError reports a mismatch between the reply and an expected HTTP
// status and error code for req.
func (r fuzzReply) expectError(label string, req fuzzRequest, status int, code int64) error {
	if r.status != status || r.errorCode() != code {
		return fmt.Errorf("%s: got %d/%d, want %d/%d: %s", label, r.status, r.errorCode(), status, code, r.body)
	}
	return r.expectID(label, req)
}

func (r fuzzReply) expectID(label string, req fuzzRequest) error {
	if !reflect.DeepEqual(r.envelope["id"], req.id) {
		return fmt.Errorf("%s: response id %v, want %v", label, r.envelope["id"], req.id)
	}
	return nil
}

// expectModernResult checks a 200 result carrying the 2026-07-28 envelope.
func (r fuzzReply) expectModernResult(label string, req fuzzRequest) error {
	result, _ := r.envelope["result"].(map[string]any)
	meta, _ := result["_meta"].(map[string]any)
	info, _ := meta["io.modelcontextprotocol/serverInfo"].(map[string]any)
	if r.status != http.StatusOK || result["resultType"] != "complete" || info["name"] != "portwing" {
		return fmt.Errorf("%s: want a 200 2026-07-28 result: %d %s", label, r.status, r.body)
	}
	return r.expectID(label, req)
}

// modernFuzzErrors replays body under 2026-07-28 and 2025-11-25 headers and
// returns every invariant the handler breaks:
//
//   - without an envelope claim, 2025-11-25 headers never change a response,
//     and a 2026-07-28 header turns a request into a -32602 rejection;
//   - with a claim, a malformed envelope is -32602, a version other than
//     2026-07-28 is -32022 naming it, contradicting or missing headers are
//     -32020, unknown methods are -32601 with 404, and results carry
//     resultType and serverInfo. Each error keeps the request id.
func modernFuzzErrors(h *Handler, body string) []error {
	var errs []error
	add := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	req := parseFuzzRequest(body)
	plain := fuzzPost(h, body, nil)

	if !req.claimed {
		legacy := fuzzPost(h, body, map[string]string{
			"MCP-Protocol-Version": "2025-11-25", "Mcp-Method": "x", "Mcp-Name": fuzzBase64Sentinel + "!?=",
		})
		if legacy.status != plain.status || !bytes.Equal(legacy.body, plain.body) {
			add(fmt.Errorf("2025-11-25 headers changed the response: %d %s, want %d %s", legacy.status, legacy.body, plain.status, plain.body))
		}
		modern := fuzzPost(h, body, req.consistentHeaders(fuzzModernVersion))
		if req.reaches {
			add(modern.expectError("2026-07-28 header without a claim", req, http.StatusBadRequest, errInvalidParams))
		} else if modern.status != plain.status || !bytes.Equal(modern.body, plain.body) {
			add(fmt.Errorf("headers changed the response to a non-request: %d %s, want %d %s", modern.status, modern.body, plain.status, plain.body))
		}
		return errs
	}
	if !req.reaches {
		return errs
	}

	version, versionIsString := req.version.(string)
	consistent := fuzzPost(h, body, req.consistentHeaders(version))
	contradicting := fuzzPost(h, body, req.consistentHeaders(version+"x"))
	wrongMethod := req.consistentHeaders(version)
	wrongMethod["Mcp-Method"] = req.method + "x"
	methodContradicting := fuzzPost(h, body, wrongMethod)
	switch {
	case !versionIsString || !req.capsOK:
		add(consistent.expectError("malformed envelope", req, http.StatusBadRequest, errInvalidParams))
		add(plain.expectError("malformed envelope without headers", req, http.StatusBadRequest, errInvalidParams))
		return errs
	case version != fuzzModernVersion:
		add(consistent.expectError("unsupported version", req, http.StatusBadRequest, errUnsupportedProtocolVersion))
		add(plain.expectError("unsupported version without headers", req, http.StatusBadRequest, errUnsupportedProtocolVersion))
		rpcErr, _ := consistent.envelope["error"].(map[string]any)
		data, _ := rpcErr["data"].(map[string]any)
		if !reflect.DeepEqual(data["supported"], []any{fuzzModernVersion}) || data["requested"] != version {
			add(fmt.Errorf("unsupported version data = %v, want supported [%s] and requested %q", data, fuzzModernVersion, version))
		}
	default:
		add(plain.expectError("no headers", req, http.StatusBadRequest, errHeaderMismatch))
		if field := fuzzNameField(req.method); field != "" {
			value, _ := decodeJSONValue(req.params[field])
			if name, ok := value.(string); ok {
				// Mcp-Name naming something else, missing, or sent raw when
				// it needed encoding: each is a header mismatch.
				for _, bad := range []string{fuzzHeaderValue(name + "x"), "", name} {
					headers := req.consistentHeaders(version)
					headers["Mcp-Name"] = bad
					if bad == "" {
						delete(headers, "Mcp-Name")
					}
					if bad == name && fuzzHeaderValue(name) == name {
						continue // a plain name sent raw is correct
					}
					add(fuzzPost(h, body, headers).expectError("bad Mcp-Name "+strconv.Quote(bad), req, http.StatusBadRequest, errHeaderMismatch))
				}
			}
		}
		switch req.method {
		case "server/discover", "tools/list":
			add(consistent.expectModernResult(req.method, req))
		case "tools/call":
			// The tool that runs must be the exact params.name the Mcp-Name
			// header mirrors, never a case-insensitive "Name" or "NAME".
			name, _ := decodeJSONValue(req.params["name"])
			if tool, isString := name.(string); isString && fuzzTools[tool] {
				add(consistent.expectModernResult(req.method, req))
			} else {
				add(consistent.expectError("tools/call protocol error", req, http.StatusOK, errInvalidParams))
			}
		default:
			add(consistent.expectError("unknown method", req, http.StatusNotFound, errMethodNotFound))
		}
	}
	add(contradicting.expectError("contradicting version header", req, http.StatusBadRequest, errHeaderMismatch))
	add(methodContradicting.expectError("contradicting method header", req, http.StatusBadRequest, errHeaderMismatch))
	return errs
}
