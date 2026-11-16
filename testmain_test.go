package main

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain is the first layer of the test binary's setup, paid once per
// process: every password the tests hash is hashed at bcrypt's minimum cost.
// At the production cost one hash is some 50 ms plain and several times that
// under the race detector, and the suite signs in several hundred times.
//
// This file is compiled into the test binary only. The shipped binary has no
// way to reach passwordCost: password_cost_guard_test.go proves it.
func TestMain(m *testing.M) {
	passwordCost = bcrypt.MinCost
	os.Exit(m.Run())
}
