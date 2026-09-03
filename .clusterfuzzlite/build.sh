#!/bin/bash -eu
# ClusterFuzzLite build script for Portwing.
#
# Runs inside the image defined by .clusterfuzzlite/Dockerfile. base-builder
# has already exported $SRC, $OUT, $WORK, $CC/$CXX, $CFLAGS/$CXXFLAGS,
# $LIB_FUZZING_ENGINE and $SANITIZER; do not set them here.
#
# Every target below is one of the ten Go native `func Fuzz*(f *testing.F)`
# functions the other tiers already run. The fuzz bodies are the same code in
# every tier; see "what a target may depend on" below for the two placement
# rules this tier adds:
#
#   tier 0  lefthook pre-push      5s per fuzzer
#   tier 1  ci-verify.yml          60s per fuzzer, every push/PR
#   tier 2  quality-fuzz-nightly   5m per fuzzer, daily
#   tier 3  quality-fuzz-monthly   1h per fuzzer, monthly
#   tier 4  ClusterFuzzLite        this file — libFuzzer + AddressSanitizer,
#                                  with a corpus that persists between runs
#
# What a target may depend on
# ---------------------------
# `compile_native_go_fuzzer` bridges a `testing.F` target to libFuzzer through
# go-118-fuzz-build, which does NOT build the package's test binary. It lifts
# the single _test.go file that declares the Fuzz function out into a normal
# package build and rewrites its `testing` import to a drop-in shim. Two rules
# follow, and both fail the build rather than degrading quietly:
#
#   1. A target must be self-contained in its own _test.go file. A helper that
#      lives in a sibling _test.go is undefined here, because that sibling is
#      never compiled. Keep the helper in the fuzz file; ordinary `go test`
#      still sees it, same package.
#   2. A target may use only the part of `testing` the shim implements. It has
#      T and F, but no TB interface, and its F has no Fatalf. A helper shared
#      between a Fuzz seeding scope and a Test should take no testing type at
#      all: return an error and let each caller report it.
#
# Both rules were found the hard way on 2026-09-03: FuzzDecodeContainerLogStream
# and FuzzComposeRequestValidate failed to build for exactly these reasons while
# passing every other tier. Nothing in internal/ outside _test.go files is
# rewritten for ClusterFuzzLite, and no fuzz body changed.
#
# The inventory here must stay identical to the one in
# quality-fuzz-nightly.yml, quality-fuzz-monthly.yml, ci-verify.yml and
# lefthook.yml. scripts/fuzz-tier-config-test.sh fails the build if it drifts.

cd "${SRC}/portwing"

module=github.com/codeswhat/portwing

# go-118-fuzz-build rewrites `func Fuzz*(f *testing.F)` into a libFuzzer entry
# point that imports github.com/AdamKorcz/go-118-fuzz-build/testing as a stand-in
# for testing.F. That import has to resolve against OUR go.mod, and the build
# fails with "no required module provides package .../go-118-fuzz-build/testing"
# without it. Verified 2026-09-03 in base-builder-go.
#
# The version is read off the generator binary in the image rather than pinned
# here or floated to @latest. Pinning skews the moment the image rebuilds with a
# newer generator; @latest skews the other way, when the image is older than the
# module. Reading the binary's own build info makes shim and generator the same
# revision by construction. This edits go.mod inside the throwaway container
# only, and nothing built here is ever released.
shim_module=github.com/AdamKorcz/go-118-fuzz-build
shim_version="$(go version -m "$(command -v go-118-fuzz-build)" |
	awk -v m="${shim_module}" '$1 == "mod" && $2 == m { print $3; exit }')"

if [ -z "${shim_version}" ]; then
	echo "build.sh: could not read the ${shim_module} version out of go-118-fuzz-build." >&2
	echo "build.sh: refusing to guess it — a mismatched shim breaks every target at once." >&2
	exit 1
fi

echo "build.sh: using ${shim_module}/testing@${shim_version} (from the image's generator)"
go get "${shim_module}/testing@${shim_version}"

# compile_native_go_fuzzer exits 0 and merely PRINTS "Could not find the
# function" when it cannot locate `func <name>(f *testing.F)`. Left alone that
# turns a renamed or deleted target into a silently shorter fuzzer list rather
# than a failed build, which is the failure this whole tier exists to notice.
# Assert the binary actually landed in $OUT.
build_fuzzer() {
	local pkg="$1"
	local fuzzer="$2"

	compile_native_go_fuzzer "${module}/${pkg}" "${fuzzer}" "${fuzzer}"

	if [ ! -x "${OUT}/${fuzzer}" ]; then
		echo "build.sh: nothing at ${OUT}/${fuzzer}." >&2
		echo "build.sh: compile_native_go_fuzzer could not find func ${fuzzer}(f *testing.F) in ${pkg}." >&2
		return 1
	fi
}

build_fuzzer internal/server FuzzParsePHC
build_fuzzer internal/server FuzzParseTrustedProxies
build_fuzzer internal/adapter FuzzParseImageRef
build_fuzzer internal/adapter/drydock FuzzParseLabels
build_fuzzer internal/mcp FuzzMCPHandler
build_fuzzer internal/protocol FuzzEnvelope
build_fuzzer internal/auth FuzzVerifyRequest
build_fuzzer internal/docker FuzzDecodeContainerLogStream
build_fuzzer internal/docker FuzzComposeRequestValidate
build_fuzzer internal/auth FuzzParseKeyLine

# No seeds are copied out of internal/*/testdata/fuzz/. Go writes those with a
# `go test fuzz v1` header and one quoted Go literal per argument; libFuzzer
# reads raw bytes, so copying them in would hand every target a corpus of
# malformed inputs. The corpus this tier runs on is the one batch fuzzing
# accumulates on the storage branch.
