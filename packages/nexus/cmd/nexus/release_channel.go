package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

var githubReleaseAPIBaseURL = "https://api.github.com"

func channelName() string {
	if v := os.Getenv("NEXUS_RELEASE_CHANNEL"); v != "" {
		return v
	}
	return "stable"
}

func channelRepo() string {
	if v := os.Getenv("NEXUS_RELEASE_REPO"); v != "" {
		return v
	}
	return "oursky/nexus"
}

func releaseBaseURL() string {
	if v := os.Getenv("NEXUS_RELEASE_BASE_URL"); v != "" {
		return v
	}

	repo := channelRepo()
	channel := channelName()

	type ghRelease struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}

	url := fmt.Sprintf("%s/repos/%s/releases", githubReleaseAPIBaseURL, repo)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Sprintf("https://github.com/%s/releases/download/latest", repo)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Sprintf("https://github.com/%s/releases/download/latest", repo)
	}

	var releases []ghRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return fmt.Sprintf("https://github.com/%s/releases/download/latest", repo)
	}

	wantPrerelease := channel == "prerelease"
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if wantPrerelease && r.Prerelease {
			return fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, r.TagName)
		}
		if !wantPrerelease && !r.Prerelease {
			return fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, r.TagName)
		}
	}

	return fmt.Sprintf("https://github.com/%s/releases/download/latest", repo)
}
