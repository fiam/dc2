package buildinfo

import (
	"runtime/debug"
	"slices"
	"strings"
	"sync"
)

const defaultVersion = "devel"

// Version can be set at link time with:
//
//	-ldflags "-X github.com/fiam/dc2/pkg/dc2/buildinfo.Version=vX.Y.Z"
var Version = defaultVersion

type Info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit,omitempty"`
	CommitTime string `json:"commit_time,omitempty"`
	Dirty      bool   `json:"dirty"`
	GoVersion  string `json:"go_version,omitempty"`
}

var (
	current     Info
	currentOnce sync.Once
	readBuild   = debug.ReadBuildInfo
)

func Current() Info {
	currentOnce.Do(func() {
		current = detect()
	})
	return current
}

func detect() Info {
	info := Info{
		Version: strings.TrimSpace(Version),
	}
	if info.Version == "" {
		info.Version = defaultVersion
	}

	buildInfo, ok := readBuild()
	if !ok || buildInfo == nil {
		return info
	}

	return applyBuildInfo(info, buildInfo)
}

func applyBuildInfo(info Info, buildInfo *debug.BuildInfo) Info {
	if buildInfo.GoVersion != "" {
		info.GoVersion = buildInfo.GoVersion
	}

	if info.Version == defaultVersion && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
		info.Version = buildInfo.Main.Version
	}

	revision, ok := buildSettingValue(buildInfo.Settings, "vcs.revision")
	if ok {
		info.Commit = revision
	}
	vcsTime, ok := buildSettingValue(buildInfo.Settings, "vcs.time")
	if ok {
		info.CommitTime = vcsTime
	}
	modified, ok := buildSettingValue(buildInfo.Settings, "vcs.modified")
	if ok {
		info.Dirty = strings.EqualFold(modified, "true")
	}

	return info
}

func buildSettingValue(settings []debug.BuildSetting, key string) (string, bool) {
	idx := slices.IndexFunc(settings, func(setting debug.BuildSetting) bool {
		return setting.Key == key
	})
	if idx < 0 {
		return "", false
	}
	value := strings.TrimSpace(settings[idx].Value)
	if value == "" {
		return "", false
	}
	return value, true
}
