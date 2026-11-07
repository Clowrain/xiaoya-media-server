package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config defines the unified configuration for Xiaoya Media Server.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Xiaoya   XiaoyaConfig   `yaml:"xiaoya"`
	Monitor  MonitorConfig  `yaml:"monitor"`
	STRM     STRMConfig     `yaml:"strm"`
	AList    AListConfig    `yaml:"alist"`
	Metadata MetadataConfig `yaml:"metadata"`
	Playback PlaybackConfig `yaml:"playback"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type DatabaseConfig struct {
	Driver             string `yaml:"driver"`
	DSN                string `yaml:"dsn"`
	BootstrapPassFile  string `yaml:"bootstrap_pass_file"`
	Host               string `yaml:"host"`
	Port               string `yaml:"port"`
	User               string `yaml:"user"`
	DBName             string `yaml:"dbname"`
	SSLMode            string `yaml:"sslmode"`
}

type XiaoyaConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
	ReadOnly   bool   `yaml:"read_only"`
}

type MonitorConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
	Roots    []string      `yaml:"roots"`
}

type STRMConfig struct {
	OutputRoot              string `yaml:"output_root"`
	Recursive               bool   `yaml:"recursive"`
	MaxPathLength           int    `yaml:"max_path_length"`
	MediaExtensions         string `yaml:"media_extensions"`
	Attachments             string `yaml:"attachments"`
	PreserveSourceStructure bool   `yaml:"preserve_source_structure"`
	TargetBaseURL           string `yaml:"target_base_url"`
}

type AListConfig struct {
	BaseURL  string `yaml:"base_url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Token    string `yaml:"token"`
}

type MetadataConfig struct {
	NFOPolicy string     `yaml:"nfo_policy"` // prefer
	TMDB      TMDBConfig `yaml:"tmdb"`
}

type TMDBConfig struct {
	Enabled  bool   `yaml:"enabled"`
	APIKey   string `yaml:"api_key"`
	Language string `yaml:"language"`
	BaseURL  string `yaml:"base_url"`
	ImageURL string `yaml:"image_url"`
}

type PlaybackConfig struct {
	Mode string `yaml:"mode"` // redirect
}

// Default returns the baseline V1.0 configuration.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Listen: "0.0.0.0:8096",
		},
		Database: DatabaseConfig{
			Driver:            "postgres",
			BootstrapPassFile: "/run/bootstrap/postgres-password",
			Host:              "postgres",
			Port:              "5432",
			User:              "emby",
			DBName:            "emby",
			SSLMode:           "disable",
		},
		Xiaoya: XiaoyaConfig{
			SQLitePath: "/xiaoya-db/strm_internal.db",
			ReadOnly:   true,
		},
		Monitor: MonitorConfig{
			Enabled:  true,
			Interval: 10 * time.Minute,
			Roots:    []string{},
		},
		STRM: STRMConfig{
			OutputRoot:              "/media/strm",
			Recursive:               true,
			MaxPathLength:           235,
			MediaExtensions:         "all",
			Attachments:             "full",
			PreserveSourceStructure: true,
			TargetBaseURL:           "http://xiaoya.host:5678",
		},
		AList: AListConfig{
			BaseURL: "http://xiaoya.host:5678",
		},
		Metadata: MetadataConfig{
			NFOPolicy: "prefer",
			TMDB: TMDBConfig{
				Enabled:  true,
				Language: "zh-CN",
				BaseURL:  "https://api.themoviedb.org/3",
				ImageURL: "https://image.tmdb.org/t/p",
			},
		},
		Playback: PlaybackConfig{
			Mode: "redirect",
		},
	}
}

// Load reads an optional YAML configuration file and overlays environment variables.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = strings.TrimSpace(os.Getenv("CONFIG_FILE"))
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	// Enforce read-only invariant on Xiaoya SQLite DB per V1.0 spec.
	cfg.Xiaoya.ReadOnly = true
	if cfg.STRM.MaxPathLength <= 0 {
		cfg.STRM.MaxPathLength = 235
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v := strings.TrimSpace(os.Getenv("LISTEN")); v != "" {
		c.Server.Listen = v
	}
	if v := strings.TrimSpace(os.Getenv("DATABASE_URL")); v != "" {
		c.Database.DSN = v
	}
	if v := strings.TrimSpace(os.Getenv("POSTGRES_HOST")); v != "" {
		c.Database.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("POSTGRES_PORT")); v != "" {
		c.Database.Port = v
	}
	if v := strings.TrimSpace(os.Getenv("POSTGRES_USER")); v != "" {
		c.Database.User = v
	}
	if v := strings.TrimSpace(os.Getenv("POSTGRES_DB")); v != "" {
		c.Database.DBName = v
	}
	if v := strings.TrimSpace(os.Getenv("XIAOYA_SQLITE_PATH")); v != "" {
		c.Xiaoya.SQLitePath = v
	}
	if v := strings.TrimSpace(os.Getenv("MONITOR_ENABLED")); v != "" {
		c.Monitor.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
	if v := strings.TrimSpace(os.Getenv("MONITOR_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.Monitor.Interval = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("MONITOR_ROOTS")); v != "" {
		parts := strings.Split(v, ",")
		roots := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" && p != "/每日更新/电视剧/国产剧（115）" && p != "/每日更新/电视剧/国产剧" {
				roots = append(roots, p)
			}
		}
		c.Monitor.Roots = roots
	}
	if v := strings.TrimSpace(os.Getenv("STRM_OUTPUT_ROOT")); v != "" {
		c.STRM.OutputRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("XIAOYA_URL")); v != "" {
		c.AList.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("ALIST_BASE_URL")); v != "" {
		c.AList.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("STRM_TARGET_BASE_URL")); v != "" {
		c.STRM.TargetBaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("XIAOYA_USERNAME")); v != "" {
		c.AList.Username = v
	}
	if v := strings.TrimSpace(os.Getenv("XIAOYA_PASSWORD")); v != "" {
		c.AList.Password = v
	}
	if v := strings.TrimSpace(os.Getenv("ALIST_TOKEN")); v != "" {
		c.AList.Token = v
	}
	if v := strings.TrimSpace(os.Getenv("TMDB_API_KEY")); v != "" {
		c.Metadata.TMDB.APIKey = v
	}
	if v := strings.TrimSpace(os.Getenv("TMDB_LANGUAGE")); v != "" {
		c.Metadata.TMDB.Language = v
	}
}

// ResolveDatabaseDSN resolves an explicit DSN or constructs one from POSTGRES_PASSWORD / bootstrap secret file.
func (c *Config) ResolveDatabaseDSN() (string, error) {
	if strings.TrimSpace(c.Database.DSN) != "" {
		return c.Database.DSN, nil
	}
	pass := strings.TrimSpace(os.Getenv("POSTGRES_PASSWORD"))
	if pass == "" && c.Database.BootstrapPassFile != "" {
		if raw, err := os.ReadFile(c.Database.BootstrapPassFile); err == nil {
			pass = strings.TrimSpace(string(raw))
		}
	}
	if pass == "" {
		return "", fmt.Errorf("database DSN is empty and neither POSTGRES_PASSWORD nor %s is available", c.Database.BootstrapPassFile)
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Database.User, pass),
		Host:   c.Database.Host + ":" + c.Database.Port,
		Path:   "/" + strings.TrimPrefix(c.Database.DBName, "/"),
	}
	q := u.Query()
	if c.Database.SSLMode != "" {
		q.Set("sslmode", c.Database.SSLMode)
	} else {
		q.Set("sslmode", "disable")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
