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
	return fmt.Sprintf("Version:\t%s\n", appVersion) +
		fmt.Sprintf("Build:\t\t%s\n", buildTime) +
		fmt.Sprintf("Git:\t\t%s\n", gitCommit) +
		fmt.Sprintf("Go:\t\t%s\n", goVersion)
}
