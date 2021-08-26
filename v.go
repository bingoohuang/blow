package main

import (
	"fmt"
)

var (
	gitCommit  = ""
	buildTime  = ""
	goVersion  = ""
	appVersion = "1.2.4"
)

func Version() string {
	return fmt.Sprintf("Version: %s\n", appVersion) +
		fmt.Sprintf("Build: %s\n", buildTime) +
		fmt.Sprintf("Git: %s\n", gitCommit) +
		fmt.Sprintf("Go: %s\n", goVersion)
}
