# The gate's venue: the Go toolchain go.mod names, plus node for the browser
# scripts' tests and the scenarios that drive them. Built once on a host and
# reused (scripts/ci/gate.sh --docker tags it by this file's hash and the Go
# version), so no run pays an apt-get again. Never shipped, never pushed.
ARG GO_VERSION
FROM golang:${GO_VERSION}
RUN apt-get update -qq \
 && apt-get install -y -qq --no-install-recommends nodejs \
 && rm -rf /var/lib/apt/lists/*
