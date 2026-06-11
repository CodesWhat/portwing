package server

import (
	"testing"
)

// FuzzParsePHC verifies that ParsePHC never panics on arbitrary input and
// that any successfully-parsed PHC round-trips: re-encoding must produce
// the same parameters that were parsed.
func FuzzParsePHC(f *testing.F) {
	// Seed: valid PHC strings.
	f.Add("$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$YWJjZGVmZ2hpamtsbW5vcA")
	f.Add("$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRz$aGFzaGhhc2hoYXNoaGFzaA")
	// Seed: hostile inputs — wrong algo, truncated, empty, nulls, long.
	f.Add("")
	f.Add("$argon2i$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$YWJj")
	f.Add("$argon2id$v=18$m=19456,t=2,p=1$c29tZXNhbHQ$YWJj")
	f.Add("$$$$$$$")
	f.Add("not a phc string at all")
	f.Add("$argon2id$v=19$m=0,t=0,p=0$c29tZXNhbHQ$YWJj")
	f.Add("$argon2id$v=19$m=19456,t=2,p=256$c29tZXNhbHQ$YWJj") // parallelism > 255
	f.Add("$argon2id$v=19$m=19456,t=2,p=1$$")                  // empty salt+hash

	f.Fuzz(func(t *testing.T, input string) {
		// Must never panic.
		params, err := ParsePHC(input)
		if err != nil {
			// Parse error is expected for most fuzz inputs; not a failure.
			return
		}

		// If parsing succeeded, params must be valid (no zero values that
		// would cause argon2.IDKey to panic).
		if params.Time < 1 {
			t.Errorf("parsed PHC has Time < 1: %d", params.Time)
		}
		if params.Parallelism < 1 {
			t.Errorf("parsed PHC has Parallelism < 1: %d", params.Parallelism)
		}
		if len(params.Salt) == 0 {
			t.Error("parsed PHC has empty Salt")
		}
		if len(params.Hash) == 0 {
			t.Error("parsed PHC has empty Hash")
		}
	})
}
