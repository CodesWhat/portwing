package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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

	// Use a nil docker client and nil collector; the handler must not reach
	// them for the JSON-RPC error paths exercised by fuzz inputs. For the
	// tools/call path that does need docker, a nil docker client will return
	// an error from the tool — which is a valid non-panic outcome.
	h := &Handler{docker: nil, collector: nil}

	f.Fuzz(func(t *testing.T, body string) {
		// Parse into raw fields independently of the production request
		// decoder so malformed envelopes cannot be mistaken for notifications.
		var requestFields map[string]json.RawMessage
		requestObject := json.Unmarshal([]byte(body), &requestFields) == nil && requestFields != nil
		requestID, hadID := requestFields["id"]
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
