// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package version carries the running server's release string. Release builds
// inject it with ldflags from the Dockerfile's VERSION build-arg; a local build
// reports 0.0.0-dev because it is none of nightly, rc or stable.
package version

// Version is set at link time. Empty means a local build.
var Version = ""

// String returns the release version, or 0.0.0-dev for a local build.
func String() string {
	if Version == "" {
		return "0.0.0-dev"
	}
	return Version
}
