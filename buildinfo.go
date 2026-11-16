package main

import "encoding/json"

// buildCommit is the commit this binary was built from. The Dockerfile sets it
// at link time (-X main.buildCommit=<sha>) from the GIT_SHA build argument; a
// plain `go build` leaves it empty.
//
// /healthz reports it so the deploy's probe can ask the running instance WHICH
// build is serving, instead of reading a 200 that the release before it would
// also have given. The repository is public and so is the commit.
var buildCommit string

// healthzBody is what /healthz answers: {"status":"ok"}, plus the commit when
// the build carries one.
func healthzBody() []byte {
	body := map[string]string{"status": "ok"}
	if buildCommit != "" {
		body["commit"] = buildCommit
	}
	b, _ := json.Marshal(body)
	return b
}
