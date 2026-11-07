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
}

export interface MediaItem {
  Id: string;
  Name: string;
  Type: string;
  ProductionYear?: number;
  PremiereDate?: string;
  Overview?: string;
  RunTimeTicks?: number;
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
}

export interface AListBrowseResult {
  path: string;
  items: AListFileItem[];
  total: number;
}

export interface TmdbConfig {
  Enabled: boolean;
  ApiKey: string;
  BaseURL: string;
  ProxyURL: string;
  RateLimitMs: number;
  AutoScrape: boolean;
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
}

export interface LogEntry {
  time?: string;
  level: string;
  msg: string;
  source?: string;
  trace_id?: string;
  [key: string]: any;
}
