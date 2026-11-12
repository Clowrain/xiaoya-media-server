package embyserver

import (
	"net/url"
	"regexp"
)

var playbackURLPattern = regexp.MustCompile(`(?i)https?://[^\s\"'<>]+`)
var playbackSecretPattern = regexp.MustCompile(`(?i)(^|[?&\s])((?:sign|token|api_key|access_token|auth|password)=)[^&\s]+`)

func redactPlaybackText(message string) string {
	message = playbackURLPattern.ReplaceAllStringFunc(message, redactPlaybackURL)
	return playbackSecretPattern.ReplaceAllString(message, "$1$2[redacted]")
}

func redactPlaybackURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[invalid playback URL]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}
