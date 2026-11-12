package embyserver

type UserDTO struct {
	ID                        string               `json:"Id"`
	Name                      string               `json:"Name"`
	ServerID                  string               `json:"ServerId"`
	HasPassword               bool                 `json:"HasPassword"`
	HasConfiguredPassword     bool                 `json:"HasConfiguredPassword"`
	HasConfiguredEasyPassword bool                 `json:"HasConfiguredEasyPassword"`
	Policy                    UserPolicyDTO        `json:"Policy"`
	Configuration             UserConfigurationDTO `json:"Configuration"`
	FirstAdmin                bool                 `json:"FirstAdmin"`
	MaxDevices                int                  `json:"MaxDevices"`
	LastLoginDate             string               `json:"LastLoginDate,omitempty"`
}

type UserPolicyDTO struct {
	IsAdministrator                  bool       `json:"IsAdministrator"`
	IsDisabled                       bool       `json:"IsDisabled"`
	IsHidden                         bool       `json:"IsHidden"`
	IsHiddenRemotely                 bool       `json:"IsHiddenRemotely"`
	IsHiddenFromUnusedDevices        bool       `json:"IsHiddenFromUnusedDevices"`
	EnableMediaPlayback              bool       `json:"EnableMediaPlayback"`
	EnableVideoPlaybackTranscoding   bool       `json:"EnableVideoPlaybackTranscoding"`
	EnableAudioPlaybackTranscoding   bool       `json:"EnableAudioPlaybackTranscoding"`
	EnablePlaybackRemuxing           bool       `json:"EnablePlaybackRemuxing"`
	EnableContentDeletion            bool       `json:"EnableContentDeletion"`
	EnableContentDownloading         bool       `json:"EnableContentDownloading"`
	EnableSubtitleDownloading        bool       `json:"EnableSubtitleDownloading"`
	EnableSubtitleManagement         bool       `json:"EnableSubtitleManagement"`
	EnableSyncTranscoding            bool       `json:"EnableSyncTranscoding"`
	EnableAllFolders                 bool       `json:"EnableAllFolders"`
	EnableAllDevices                 bool       `json:"EnableAllDevices"`
	EnableAllChannels                bool       `json:"EnableAllChannels"`
	EnableRemoteAccess               bool       `json:"EnableRemoteAccess"`
	EnableRemoteControlOfOtherUsers  bool       `json:"EnableRemoteControlOfOtherUsers"`
	EnableSharedDeviceControl        bool       `json:"EnableSharedDeviceControl"`
	EnableLiveTVAccess               bool       `json:"EnableLiveTvAccess"`
	EnableLiveTVManagement           bool       `json:"EnableLiveTvManagement"`
	EnablePublicSharing              bool       `json:"EnablePublicSharing"`
	EnableMediaConversion            bool       `json:"EnableMediaConversion"`
	EnableUserPreferenceAccess       bool       `json:"EnableUserPreferenceAccess"`
	BlockedTags                      []string   `json:"BlockedTags"`
	IncludeTags                      []string   `json:"IncludeTags"`
	AccessSchedules                  []struct{} `json:"AccessSchedules"`
	BlockUnratedItems                []string   `json:"BlockUnratedItems"`
	EnabledFolders                   []string   `json:"EnabledFolders"`
	EnabledDevices                   []string   `json:"EnabledDevices"`
	EnabledChannels                  []string   `json:"EnabledChannels"`
	ExcludedSubFolders               []string   `json:"ExcludedSubFolders"`
	EnableContentDeletionFromFolders []string   `json:"EnableContentDeletionFromFolders"`
	RemoteClientBitrateLimit         int        `json:"RemoteClientBitrateLimit"`
	InvalidLoginAttemptCount         int        `json:"InvalidLoginAttemptCount"`
	SimultaneousStreamLimit          int        `json:"SimultaneousStreamLimit"`
}

type UserConfigurationDTO struct {
	OrderedViews               []string `json:"OrderedViews"`
	DisplayMissingEpisodes     bool     `json:"DisplayMissingEpisodes"`
	PlayDefaultAudioTrack      bool     `json:"PlayDefaultAudioTrack"`
	SubtitleMode               string   `json:"SubtitleMode"`
	LatestItemsExcludes        []string `json:"LatestItemsExcludes"`
	MyMediaExcludes            []string `json:"MyMediaExcludes"`
	HidePlayedInLatest         bool     `json:"HidePlayedInLatest"`
	HidePlayedInMoreLikeThis   bool     `json:"HidePlayedInMoreLikeThis"`
	HidePlayedInSuggestions    bool     `json:"HidePlayedInSuggestions"`
	RememberAudioSelections    bool     `json:"RememberAudioSelections"`
	RememberSubtitleSelections bool     `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  bool     `json:"EnableNextEpisodeAutoPlay"`
	ResumeRewindSeconds        int      `json:"ResumeRewindSeconds"`
	IntroSkipMode              string   `json:"IntroSkipMode"`
	EnableLocalPassword        bool     `json:"EnableLocalPassword"`
}

func (a *App) userDTO(user User) UserDTO {
	return UserDTO{
		ID:                        user.ID,
		Name:                      user.Name,
		ServerID:                  a.serverID,
		HasPassword:               true,
		HasConfiguredPassword:     true,
		HasConfiguredEasyPassword: false,
		Policy: UserPolicyDTO{
			IsAdministrator:                  user.Admin,
			IsHidden:                         true,
			IsHiddenRemotely:                 true,
			IsHiddenFromUnusedDevices:        true,
			EnableMediaPlayback:              a.canPlay(user.ID),
			EnableAllFolders:                 true,
			EnableAllDevices:                 true,
			EnableRemoteAccess:               true,
			BlockedTags:                      []string{},
			IncludeTags:                      []string{},
			AccessSchedules:                  []struct{}{},
			BlockUnratedItems:                []string{},
			EnabledFolders:                   []string{},
			EnabledDevices:                   []string{},
			EnabledChannels:                  []string{},
			ExcludedSubFolders:               []string{},
			EnableContentDeletionFromFolders: []string{},
			SimultaneousStreamLimit:          user.Max,
		},
		Configuration: UserConfigurationDTO{
			OrderedViews:              a.orderedViews(),
			PlayDefaultAudioTrack:     true,
			SubtitleMode:              "Smart",
			LatestItemsExcludes:       []string{},
			MyMediaExcludes:           []string{},
			EnableNextEpisodeAutoPlay: true,
			IntroSkipMode:             "ShowButton",
		},
		FirstAdmin: user.First,
		MaxDevices: user.Max,
	}
}
