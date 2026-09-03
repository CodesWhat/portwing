# ClusterFuzzLite builder image for Portwing.
#
# Unlike OSS-Fuzz, ClusterFuzzLite copies the source in during `docker build`
# rather than cloning it here: the action clones the repo on the runner, checks
# out the ref under test, and builds this file with that checkout as context.
# The root `.dockerignore` therefore applies, which is what keeps `docs/`,
# `website/`, `node_modules/` and `.git/` out of the context.
#
# base-builder-go currently ships Go 1.25.0 while go.mod asks for 1.26.0 with
# `toolchain go1.27.0`. GOTOOLCHAIN is `auto` in this image, so the go command
# downloads go1.27.0 from the module proxy on first use. That is why
# proxy.golang.org and sum.golang.org are in the egress allow-lists of both
# ClusterFuzzLite workflows — without them the toolchain switch fails before a
# single fuzzer is built.
#
# Pinned by digest to match the convention in the repo's other Dockerfiles.
# Renovate tracks it. Note this is the only ClusterFuzzLite image that CAN be
# pinned: the two actions are Docker container actions whose `runs.image` is a
# floating `gcr.io/oss-fuzz-base/clusterfuzzlite-*:v1` tag inside Google's
# action definition, so pinning the action to a commit SHA does not pin what
# actually executes.
FROM gcr.io/oss-fuzz-base/base-builder-go:v1@sha256:e5e5ecf4fb18a2f47bf0462c37e35728e8f6023ee4b7ff8bc5f903e6acba46da

COPY . $SRC/portwing
COPY ./.clusterfuzzlite/build.sh $SRC/
WORKDIR $SRC/portwing