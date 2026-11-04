// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
)

var signatureLockUID = os.Geteuid

func printSignatureLockUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: portwing signature-lock list")
	fmt.Fprintln(w, "       portwing signature-lock clear <keyID>")
	fmt.Fprintln(w, "       portwing signature-lock clear --all")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manages the downgrade lock that refuses a key's v2 and legacy signatures once")
	fmt.Fprintln(w, "it has signed a v3 request. Clearing a lock lets that key sign v2 again, for")
	fmt.Fprintln(w, "example after rolling the controller back to a release without v3.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Reads and writes the lock file in STATE_DIR (default KEY_ROTATION_DIR, then")
	fmt.Fprintln(w, "/data/credentials). A running agent picks up a clear on its next request, with")
	fmt.Fprintln(w, "no restart. Changes are written to the AUDIT_LOG destination when one is set.")
}

// runSignatureLock implements `portwing signature-lock`. Usage errors exit 2,
// failures exit 1.
func runSignatureLock(args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		printSignatureLockUsage(stderr)
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return usage()
		}
	case "clear":
		if len(args) != 2 || (args[1] != "--all" && !auth.ValidKeyID(args[1])) {
			return usage()
		}
	default:
		return usage()
	}

	dir, _ := config.ResolveStateDir()
	locks, err := auth.OpenSignatureLocks(dir)
	if err != nil {
		fmt.Fprintf(stderr, "signature-lock: %v\n", err)
		return 1
	}

	if args[0] == "list" {
		list := locks.List()
		if len(list) == 0 {
			fmt.Fprintln(stdout, "no keys are locked to signature version 3")
			return 0
		}
		for _, l := range list {
			fmt.Fprintf(stdout, "%s locked to v3 since %s\n", l.KeyID, l.Since.UTC().Format(time.RFC3339))
		}
		return 0
	}
	if err := checkSignatureLockOwner(dir, signatureLockUID()); err != nil {
		fmt.Fprintf(stderr, "signature-lock: %v\n", err)
		return 1
	}

	auditor, closeAudit, err := audit.New(os.Getenv("AUDIT_LOG"), 0)
	if err != nil {
		fmt.Fprintf(stderr, "signature-lock: opening audit log: %v\n", err)
		return 1
	}
	defer closeAudit()

	if args[1] == "--all" {
		n, err := locks.ClearAll()
		if err != nil {
			auditor.SignatureLock("cli", "clear_all", "", audit.OutcomeError)
			fmt.Fprintf(stderr, "signature-lock: %v\n", err)
			return 1
		}
		if n > 0 {
			auditor.SignatureLock("cli", "clear_all", "", audit.OutcomeAllowed)
		}
		fmt.Fprintf(stdout, "cleared %d signature lock(s)\n", n)
		return 0
	}

	cleared, err := locks.Clear(args[1])
	if err != nil {
		auditor.SignatureLock("cli", "clear", args[1], audit.OutcomeError)
		fmt.Fprintf(stderr, "signature-lock: %v\n", err)
		return 1
	}
	if !cleared {
		fmt.Fprintf(stdout, "no lock for key %s\n", args[1])
		return 0
	}
	auditor.SignatureLock("cli", "clear", args[1], audit.OutcomeAllowed)
	fmt.Fprintf(stdout, "cleared the signature lock for key %s\n", args[1])
	return 0
}
