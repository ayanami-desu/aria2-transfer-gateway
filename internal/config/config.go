package config

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr  string         `yaml:"listen_addr"`
	DataFile    string         `yaml:"data_file"`
	DownloadDir string         `yaml:"download_dir"`
	WorkerCount int            `yaml:"worker_count"`
	CORSOrigins []string       `yaml:"cors_origins"`
	API         APIConfig      `yaml:"api"`
	Aria2       Aria2Config    `yaml:"aria2"`
	Trackers    TrackersConfig `yaml:"trackers"`
	ED2K        ED2KConfig     `yaml:"ed2k"`
}

type TrackersConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`
	Interval string `yaml:"interval"`
}

type ED2KConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`
	Interval string `yaml:"interval"`
}

type APIConfig struct {
	Token         string `yaml:"token"`
	GatewaySecret string `yaml:"gateway_secret"`
}

type Aria2Config struct {
	Endpoint      string `yaml:"endpoint"`
	Secret        string `yaml:"secret"`
	AriaRPCSecret string `yaml:"aria_rpc_secret"`
	CompleteHook  string `yaml:"complete_hook"`
	StoppedHook   string `yaml:"stopped_hook"`
}

type Runtime struct {
	Config          Config
	APIToken        string
	Aria2Secret     string
	TrackerInterval time.Duration
	ED2KInterval    time.Duration
}

func Default() Config {
	return Config{
		ListenAddr:  "127.0.0.1:8787",
		DataFile:    "./data/tasks.db",
		DownloadDir: "./data/downloads",
		WorkerCount: 2,
		CORSOrigins: []string{"*"},
		API: APIConfig{
			GatewaySecret: "GATEWAY_API_TOKEN",
		},
		Aria2: Aria2Config{
			Endpoint:      "http://127.0.0.1:6800/jsonrpc",
			AriaRPCSecret: "ARIA2_RPC_SECRET",
		},
		Trackers: TrackersConfig{Interval: "24h"},
		ED2K: ED2KConfig{
			URL:      "http://upd.emule-security.org/server.met",
			Interval: "24h",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	defaults := Default()
	if c.ListenAddr == "" {
		c.ListenAddr = defaults.ListenAddr
	}
	if c.DataFile == "" {
		c.DataFile = defaults.DataFile
	}
	if c.DownloadDir == "" {
		c.DownloadDir = defaults.DownloadDir
	}
	if c.WorkerCount <= 0 {
		c.WorkerCount = defaults.WorkerCount
	}
	if len(c.CORSOrigins) == 0 {
		c.CORSOrigins = defaults.CORSOrigins
	}
	if c.API.GatewaySecret == "" && c.API.Token == "" {
		c.API.GatewaySecret = defaults.API.GatewaySecret
	}
	if c.Aria2.Endpoint == "" {
		c.Aria2.Endpoint = defaults.Aria2.Endpoint
	}
	if c.Aria2.AriaRPCSecret == "" && c.Aria2.Secret == "" {
		c.Aria2.AriaRPCSecret = defaults.Aria2.AriaRPCSecret
	}
	if c.Trackers.Interval == "" {
		c.Trackers.Interval = defaults.Trackers.Interval
	}
	if c.ED2K.URL == "" {
		c.ED2K.URL = defaults.ED2K.URL
	}
	if c.ED2K.Interval == "" {
		c.ED2K.Interval = defaults.ED2K.Interval
	}
}

func (c Config) Resolve() (Runtime, error) {
	interval := 24 * time.Hour
	if c.Trackers.Interval != "" {
		parsed, err := time.ParseDuration(c.Trackers.Interval)
		if err != nil {
			return Runtime{}, fmt.Errorf("invalid trackers.interval: %w", err)
		}
		if parsed <= 0 {
			return Runtime{}, fmt.Errorf("trackers.interval must be positive")
		}
		interval = parsed
	}
	if c.Trackers.Enabled {
		parsed, err := url.Parse(c.Trackers.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Runtime{}, fmt.Errorf("trackers.url must be a valid http or https URL with a host")
		}
	}
	ed2kInterval := 24 * time.Hour
	if c.ED2K.Interval != "" {
		parsed, err := time.ParseDuration(c.ED2K.Interval)
		if err != nil {
			return Runtime{}, fmt.Errorf("invalid ed2k.interval: %w", err)
		}
		if parsed <= 0 {
			return Runtime{}, fmt.Errorf("ed2k.interval must be positive")
		}
		ed2kInterval = parsed
	}
	if c.ED2K.Enabled {
		parsed, err := url.Parse(c.ED2K.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Runtime{}, fmt.Errorf("ed2k.url must be a valid http or https URL with a host")
		}
	}
	apiToken := c.API.Token
	if apiToken == "" && c.API.GatewaySecret != "" {
		apiToken = os.Getenv(c.API.GatewaySecret)
	}
	aria2Secret := c.Aria2.Secret
	if aria2Secret == "" && c.Aria2.AriaRPCSecret != "" {
		aria2Secret = os.Getenv(c.Aria2.AriaRPCSecret)
	}
	return Runtime{
		Config:          c,
		APIToken:        apiToken,
		Aria2Secret:     aria2Secret,
		TrackerInterval: interval,
		ED2KInterval:    ed2kInterval,
	}, nil
}
