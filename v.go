package main

import (
	"fmt"
)

var (
	gitCommit  = ""
	buildTime  = ""
	goVersion  = ""
	appVersion = "1.4.0"
)

func Version() string {
	return fmt.Sprintf("version: %s\n", appVersion) +
		fmt.Sprintf("build:\t%s\n", buildTime) +
		fmt.Sprintf("git:\t%s\n", gitCommit) +
		fmt.Sprintf("go:\t%s\n", goVersion)
}
