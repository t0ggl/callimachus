package main

import (
	"regexp"
	"runtime/debug"
)

var (
	// SemVer numeric identifiers disallow leading zeroes except in build metadata.
	releaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){2}(-` + prereleaseID + `(\.` + prereleaseID + `)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	gitSHA     = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
)

const prereleaseID = `(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)`

func resolveDisplayVersion(version, commit string, info *debug.BuildInfo) string {
	if releaseTag.MatchString(version) {
		return version
	}
	if gitSHA.MatchString(commit) {
		return commit[:min(len(commit), 8)]
	}
	if info != nil {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && gitSHA.MatchString(setting.Value) {
				return setting.Value[:min(len(setting.Value), 8)]
			}
		}
	}
	return "dev"
}
