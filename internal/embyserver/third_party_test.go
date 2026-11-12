package embyserver

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestThirdPartyPlaybackPrefixAndAuthentication(t *testing.T) {
	a := testApp(t)
	var uid string
	if err := a.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{"INSERT INTO libraries(id,name,path,kind) VALUES('l','test','/media/test','movies')", "INSERT INTO items(id,lib,parent,name,kind,path,url,seen) VALUES('i','l','l','movie','Movie','/media/test/a.strm','http://origin/movie.mp4','g')"} {
		if _, err := a.db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.Exec("INSERT INTO tokens VALUES(?,?,?,?)", digest("viewer"), uid, "phone", time.Now().Unix()+60); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUBLIC_PLAYBACK_URL", "http://gateway:7799")
	for _, prefix := range []string{"", "/emby", "/emby/emby", "/EMBY/emby"} {
		w := httptest.NewRecorder()
		a.serve(w, httptest.NewRequest("POST", prefix+"/Items/i/PlaybackInfo?api_key=viewer", strings.NewReader(`{}`)))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", prefix, w.Code, w.Body.String())
		}
		var info struct {
			MediaSources []struct {
				DirectStreamUrl string
				Path            string
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		src := info.MediaSources[0]
		if strings.HasPrefix(src.DirectStreamUrl, "/emby/") {
			t.Fatal("relative stream path must not duplicate client base prefix")
		}
		if !strings.HasPrefix(src.Path, "http://") {
			t.Fatal("direct-play Path must remain absolute")
		}
		for _, method := range []string{"GET", "HEAD"} {
			r := httptest.NewRequest(method, prefix+src.DirectStreamUrl, nil)
			w = httptest.NewRecorder()
			a.serve(w, r)
			if w.Code != 302 {
				t.Fatalf("stream %s %s: %d", method, prefix, w.Code)
			}
			u, err := url.Parse(w.Header().Get("Location"))
			if err != nil || u.Host != "gateway:7799" {
				t.Fatal("stream bypassed gateway")
			}
		}
		for _, token := range []string{"", "invalid", "viewer"} {
			r := httptest.NewRequest("GET", "/auth/playback", nil)
			r.Header.Set("X-Go-Emby-URI", prefix+"/Videos/i/stream.mp4?api_key="+token)
			w = httptest.NewRecorder()
			a.serve(w, r)
			want := 401
			if token == "viewer" {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("auth %s: got %d want %d", prefix, w.Code, want)
			}
		}
	}
}

func TestThirdPartyUserConfigurationShape(t *testing.T) {
	a := testApp(t)
	user := a.userDTO(User{})
	config := user.Configuration
	if config.SubtitleMode != "Smart" {
		t.Fatal("missing subtitle mode")
	}
	if !config.PlayDefaultAudioTrack || !config.EnableNextEpisodeAutoPlay || config.RememberAudioSelections || config.RememberSubtitleSelections {
		t.Fatalf("unexpected configuration capabilities: %+v", config)
	}
	b, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"LatestItemsExcludes":null`) || strings.Contains(string(b), `"EnabledDevices":null`) {
		t.Fatal("client arrays must not be null")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Id", "Name", "ServerId", "HasPassword", "HasConfiguredPassword", "HasConfiguredEasyPassword", "Policy", "Configuration", "FirstAdmin", "MaxDevices"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing protocol field %s: %s", key, b)
		}
	}
	var policyFields, configFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["Policy"], &policyFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fields["Configuration"], &configFields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"IsAdministrator", "IsDisabled", "IsHidden", "IsHiddenRemotely", "IsHiddenFromUnusedDevices", "EnableMediaPlayback", "EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableContentDeletion", "EnableContentDownloading", "EnableSubtitleDownloading", "EnableSubtitleManagement", "EnableSyncTranscoding", "EnableAllFolders", "EnableAllDevices", "EnableAllChannels", "EnableRemoteAccess", "EnableRemoteControlOfOtherUsers", "EnableSharedDeviceControl", "EnableLiveTvAccess", "EnableLiveTvManagement", "EnablePublicSharing", "EnableMediaConversion", "EnableUserPreferenceAccess", "BlockedTags", "IncludeTags", "AccessSchedules", "BlockUnratedItems", "EnabledFolders", "EnabledDevices", "EnabledChannels", "ExcludedSubFolders", "EnableContentDeletionFromFolders", "RemoteClientBitrateLimit", "InvalidLoginAttemptCount", "SimultaneousStreamLimit"} {
		if _, ok := policyFields[key]; !ok {
			t.Fatalf("missing policy field %s: %s", key, b)
		}
	}
	for _, key := range []string{"OrderedViews", "DisplayMissingEpisodes", "PlayDefaultAudioTrack", "SubtitleMode", "LatestItemsExcludes", "MyMediaExcludes", "HidePlayedInLatest", "HidePlayedInMoreLikeThis", "HidePlayedInSuggestions", "RememberAudioSelections", "RememberSubtitleSelections", "EnableNextEpisodeAutoPlay", "ResumeRewindSeconds", "IntroSkipMode", "EnableLocalPassword"} {
		if _, ok := configFields[key]; !ok {
			t.Fatalf("missing configuration field %s: %s", key, b)
		}
	}
	policy := user.Policy
	if policy.EnableContentDownloading || policy.EnableSubtitleDownloading || policy.EnablePlaybackRemuxing || policy.EnableVideoPlaybackTranscoding {
		t.Fatalf("unexpected playback capabilities: %+v", policy)
	}
}

func TestServerDomainsExtensionEndpoint(t *testing.T) {
	a := &App{}
	for _, p := range []string{"/emby/System/Ext/ServerDomains", "/System/Ext/ServerDomains", "/emby/system/ext/serverdomains"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", p, nil)
		r.Host = "192.168.1.100:8096"
		a.serve(w, r)
		if w.Code != 200 {
			t.Fatalf("path %s returned %d: %s", p, w.Code, w.Body.String())
		}
		var resp struct {
			Ok   bool `json:"ok"`
			Data []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if !resp.Ok || len(resp.Data) == 0 {
			t.Fatalf("expected ok=true and non-empty lines, got %+v", resp)
		}
		if resp.Data[0].URL != "http://192.168.1.100:8096" {
			t.Fatalf("unexpected line url: %s", resp.Data[0].URL)
		}
	}
}
