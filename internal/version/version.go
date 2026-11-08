package version

import "time"

var (
	// Version is the current semantic release version of Xiaoya Media Server
	Version = "v2.1.0"
	// GitCommit is the git commit hash / build number
	GitCommit = "dev"
	// BuildTime is the build timestamp
	BuildTime = ""
)

func init() {
	if BuildTime == "" || BuildTime == "unknown" {
		BuildTime = time.Now().Format("2006-01-02 15:04:05")
	}
}
