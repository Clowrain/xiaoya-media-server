export interface User {
  Id: string;
  Name: string;
  Policy?: {
    IsAdministrator: boolean;
    EnableMediaPlayback: boolean;
    EnableContentDownloading: boolean;
  };
}

export interface LibraryView {
  Id: string;
  Name: string;
  CollectionType?: string;
  Type?: string;
  ImageTags?: {
    Primary?: string;
    [key: string]: string | undefined;
  };
  BackdropImageTags?: string[];
}

export interface MediaItem {
  Id: string;
  Name: string;
  Type: string;
  ProductionYear?: number;
  PremiereDate?: string;
  Overview?: string;
  RunTimeTicks?: number;
  IndexNumber?: number;
  ParentIndexNumber?: number;
  SeriesName?: string;
  SeriesId?: string;
  SeasonName?: string;
  SeasonId?: string;
  ImageTags?: {
    Primary?: string;
    Backdrop?: string;
    Logo?: string;
    Thumb?: string;
  };
  BackdropImageTags?: string[];
  Genres?: string[];
  CommunityRating?: number;
  MediaSources?: MediaSource[];
  Path?: string;
  IsFolder?: boolean;
  ChildCount?: number;
  RecursiveItemCount?: number;
  EpisodeCount?: number;
  UserData?: {
    PlaybackPositionTicks?: number;
    Played?: boolean;
    PlayCount?: number;
    UnplayedItemCount?: number;
  };
}

export interface MediaSource {
  Id: string;
  Name: string;
  Path?: string;
  Container?: string;
  MediaStreams?: {
    Type: string;
    Codec?: string;
    Language?: string;
    DisplayTitle?: string;
  }[];
}

export interface AdminLibrary {
  Id: string;
  ItemId?: string;
  Name: string;
  Kind?: string;
  CollectionType?: string;
  Path?: string;
  Paths?: string[];
  Locations?: string[];
  Items?: number;
  Count?: number;
  Status?: string;
  Scanned?: number;
  Error?: string;
  Duration?: number;
}

export interface SystemOverview {
  mode?: string;
  cache_db_path?: string;
  cache_db_size_bytes?: number;
  strm_count?: number;
  alist_host?: string;
  alist_base_url?: string;
  tmdb_available?: boolean;
  tmdb_source?: string;
  recent_logs?: string[];
  status?: string;
  resources?: {
    active_files?: number;
    missing_files?: number;
    failed_files?: number;
  };
  [key: string]: any;
}

export interface MonitorRoot {
  id: string;
  root_path: string;
  interval_sec: number;
  enabled: boolean;
  last_scan_time?: string;
  last_status?: string;
  last_error?: string;
  item_count?: number;
}

export interface AListFileItem {
  name: string;
  size: number;
  is_dir: boolean;
  modified?: string;
  sign?: string;
  thumb?: string;
  type?: number;
  full_path?: string;
  parent?: string;
  source_node_id?: number;
  in_sqlite?: boolean;
}

export interface AListBrowseResult {
  path: string;
  items: AListFileItem[];
  total: number;
  source?: 'sqlite' | 'alist';
  parent_id?: number;
}

export interface TmdbConfig {
  Enabled: boolean;
  ApiKey: string;
  BaseURL: string;
  ProxyURL: string;
  RateLimitMs: number;
  AutoScrape: boolean;
  SaveArtifactsToMediaDir?: boolean;
}

export interface TmdbSearchResult {
  id: number;
  title: string;
  name?: string;
  overview: string;
  poster_path?: string;
  backdrop_path?: string;
  release_date?: string;
  first_air_date?: string;
  media_type?: string;
  year?: number;
  poster_url?: string;
  backdrop_url?: string;
  original_title?: string;
  rating?: number;
}

export interface RecognitionPending {
  id: string;
  resource_id: string;
  source_path: string;
  media_type: string;
  title: string;
  year: number;
  season_number: number;
  episode_number: number;
  state: string;
  evidence: string;
  external_id?: string;
  updated_at: number;
}

export interface LogEntry {
  time?: string;
  level: string;
  msg: string;
  source?: string;
  trace_id?: string;
  [key: string]: any;
}


export interface SyncBatch {
  id: string;
  root_path: string;
  status: string;
  total_seen: number;
  added_count: number;
  modified_count: number;
  renamed_count: number;
  moved_count: number;
  missing_count: number;
  restored_count: number;
  failed_count: number;
  unindexed_strm_count?: number;
  error_message?: string;
  started_at: number;
}

export interface AdminJob {
  id: string;
  kind: 'sync' | 'import' | 'task';
  scope: string;
  status: 'RUNNING' | 'SUCCESS' | 'PARTIAL' | 'FAILED' | 'INTERRUPTED';
  created_at: number;
  error?: string;
  result?: {
    source_path?: string;
    errors?: string[];
    batches?: SyncBatch[];
    imported_files?: number;
    imported_dirs?: number;
    failed_files?: number;
    generated_strms?: string[];
    unindexed_strms?: string[];
  };
}
