// SPDX-License-Identifier: MIT

package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// publicRoutePatterns are the registered patterns that are deliberately
// reachable without credentials. Everything else registerRoutes wires up must
// answer 401 to an anonymous request, so a new route that forgets authWrap
// fails the table below until someone adds it here on purpose.
var publicRoutePatterns = []string{
	"GET /_portwing/health",
	"GET /ready",
	"GET /health",
	"/api/portwing/enroll",
}

// registeredRoutePatterns reads the mux.Handle and mux.HandleFunc patterns out
// of registerRoutes in http.go, so the list cannot drift from the router.
func registeredRoutePatterns(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "http.go", nil, 0)
	if err != nil {
		t.Fatalf("parse http.go: %v", err)
	}
	var patterns []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "registerRoutes" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "mux" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Fatalf("mux.%s pattern at %v is not a string literal; extend the table", sel.Sel.Name, lit)
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			patterns = append(patterns, pattern)
			return true
		})
	}
	return patterns
}

func TestEveryRegisteredRouteRequiresAuthUnlessPublic(t *testing.T) {
	t.Parallel()

	patterns := registeredRoutePatterns(t)
	// Guards the parse itself: a refactor that hides the registrations from it
	// must not turn the table into an empty pass.
	for _, must := range []string{
		"GET /_portwing/info", "POST /_portwing/compose", "GET /_portwing/metrics", "GET /metrics",
		"GET /_portwing/audit", "GET /_portwing/audit/export", "/_portwing/mcp", "/_portwing/auth/rotation", "/",
	} {
		if !slices.Contains(patterns, must) {
			t.Fatalf("registerRoutes patterns %q do not include %q", patterns, must)
		}
	}
	for _, pub := range publicRoutePatterns {
		if !slices.Contains(patterns, pub) {
			t.Errorf("public allow-list entry %q is not registered; remove it", pub)
		}
	}

	_, handler := newRouteTestServer(t, routeTestServerOpts{token: "secret"})

	for i, pattern := range patterns {
		if slices.Contains(publicRoutePatterns, pattern) {
			continue
		}
		method, path := http.MethodPost, pattern
		if m, p, ok := strings.Cut(pattern, " "); ok {
			method, path = m, p
		}
		t.Run(pattern, func(t *testing.T) {
			req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			// A distinct client per request keeps the per-IP failure budget
			// from turning later 401s into 429s.
			req.RemoteAddr = fmt.Sprintf("192.0.2.%d:4000", i+1)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without credentials = %d, want 401", method, path, rec.Code)
			}
		})
	}
}
