package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveDisplayVersion(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	const revision = "abcdef0123456789abcdef0123456789abcdef01"
	vcs := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.modified", Value: "true"},
	}}
	invalidVCS := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "private server detail"},
		{Key: "other", Value: revision},
	}}

	tests := []struct {
		name    string
		version string
		commit  string
		info    *debug.BuildInfo
		want    string
	}{
		{"release tag wins over SHAs", "v0.7.0", commit, vcs, "v0.7.0"},
		{"release tag without SHA", "v1.2.3", "none", nil, "v1.2.3"},
		{"prerelease and build metadata", "v1.2.3-rc.1+build.001", commit, nil, "v1.2.3-rc.1+build.001"},
		{"branch prefers linker SHA", "main", commit, vcs, "01234567"},
		{"dev with short SHA", "dev", "a1b2c3d", nil, "a1b2c3d"},
		{"empty version with SHA", "", commit, nil, "01234567"},
		{"version prefix alone", "vnext", commit, nil, "01234567"},
		{"incomplete semver", "v1.2", commit, nil, "01234567"},
		{"missing v prefix", "1.2.3", commit, nil, "01234567"},
		{"leading zero in core version", "v01.2.3", commit, nil, "01234567"},
		{"leading zero in numeric prerelease", "v1.2.3-01", commit, nil, "01234567"},
		{"empty build identifier", "v1.2.3+build..1", commit, nil, "01234567"},
		{"default linker commit uses VCS", "dev", "none", vcs, "abcdef01"},
		{"empty linker commit uses VCS", "", "", vcs, "abcdef01"},
		{"invalid linker commit uses VCS", "main", "invalid", vcs, "abcdef01"},
		{"absent VCS metadata", "main", "none", &debug.BuildInfo{}, "dev"},
		{"invalid VCS revision is ignored", "main", "none", invalidVCS, "dev"},
		{"untagged build without metadata", "", "", nil, "dev"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveDisplayVersion(test.version, test.commit, test.info)
			if got != test.want {
				t.Errorf("resolveDisplayVersion(%q, %q) = %q, want %q", test.version, test.commit, got, test.want)
			}
		})
	}
}
