package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"aria2-transfer-gateway/internal/domain"
)

type Config struct {
	ListenAddr           string              `yaml:"listen_addr"`
	DataFile             string              `yaml:"data_file"`
	DownloadDir          string              `yaml:"download_dir"`
	WorkerCount          int                 `yaml:"worker_count"`
	DefaultDestinationID string              `yaml:"default_destination_id"`
	CORSOrigins          []string            `yaml:"cors_origins"`
	API                  APIConfig           `yaml:"api"`
	Aria2                Aria2Config         `yaml:"aria2"`
	Destinations         []DestinationConfig `yaml:"destinations"`
}

type APIConfig struct {
	Token    string `yaml:"token"`
	TokenEnv string `yaml:"token_env"`
}

type Aria2Config struct {
	Endpoint     string `yaml:"endpoint"`
	Secret       string `yaml:"secret"`
	SecretEnv    string `yaml:"secret_env"`
	CompleteHook string `yaml:"complete_hook"`
	StoppedHook  string `yaml:"stopped_hook"`
}

type DestinationConfig struct {
	ID              string `yaml:"id"`
	Name            string `yaml:"name"`
	Provider        string `yaml:"provider"`
	Endpoint        string `yaml:"endpoint"`
	Mount           string `yaml:"mount"`
	Remote          string `yaml:"remote"`
	Root            string `yaml:"root"`
	RcloneConfig    string `yaml:"rclone_config"`
	RcloneConfigEnv string `yaml:"rclone_config_env"`
	Token           string `yaml:"token"`
	TokenEnv        string `yaml:"token_env"`
	Proxy           string `yaml:"proxy"`
	ProxyEnv        string `yaml:"proxy_env"`
}

type Runtime struct {
	Config               Config
	APIToken             string
	Aria2Secret          string
	DefaultDestinationID string
	Destinations         []domain.Destination
}

func Default() Config {
	return Config{
		ListenAddr:  "127.0.0.1:8787",
		DataFile:    "./data/tasks.db",
		DownloadDir: "./data/downloads",
		WorkerCount: 2,
		CORSOrigins: []string{"*"},
		Aria2: Aria2Config{
			Endpoint:  "http://127.0.0.1:6800/jsonrpc",
			SecretEnv: "ARIA2_RPC_SECRET",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyEnvironment()
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyEnvironment() {
	raw := strings.TrimSpace(os.Getenv("GATEWAY_CORS_ORIGINS"))
	if raw == "" {
		return
	}

	origins := make([]string, 0, strings.Count(raw, ",")+1)
	for _, origin := range strings.Split(raw, ",") {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) > 0 {
		c.CORSOrigins = origins
	}
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
	if c.Aria2.Endpoint == "" {
		c.Aria2.Endpoint = defaults.Aria2.Endpoint
	}
	if c.Aria2.SecretEnv == "" && c.Aria2.Secret == "" {
		c.Aria2.SecretEnv = defaults.Aria2.SecretEnv
	}
}

func (c Config) Resolve() (Runtime, error) {
	apiToken := c.API.Token
	if apiToken == "" && c.API.TokenEnv != "" {
		apiToken = os.Getenv(c.API.TokenEnv)
	}
	aria2Secret := c.Aria2.Secret
	if aria2Secret == "" && c.Aria2.SecretEnv != "" {
		aria2Secret = os.Getenv(c.Aria2.SecretEnv)
	}

	defaultDestinationID := strings.TrimSpace(c.DefaultDestinationID)
	seen := make(map[string]struct{}, len(c.Destinations))
	destinations := make([]domain.Destination, 0, len(c.Destinations))
	for _, item := range c.Destinations {
		if item.ID == "" || item.Name == "" || item.Provider == "" {
			return Runtime{}, fmt.Errorf("destination requires id, name, and provider")
		}
		if _, exists := seen[item.ID]; exists {
			return Runtime{}, fmt.Errorf("duplicate destination id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		rcloneConfig := item.RcloneConfig
		if rcloneConfig == "" && item.RcloneConfigEnv != "" {
			rcloneConfig = os.Getenv(item.RcloneConfigEnv)
		}
		token := item.Token
		if token == "" && item.TokenEnv != "" {
			token = os.Getenv(item.TokenEnv)
		}
		proxy := item.Proxy
		if proxy == "" && item.ProxyEnv != "" {
			proxy = os.Getenv(item.ProxyEnv)
		}
		proxy, err := domain.NormalizeProxyURL(proxy)
		if err != nil {
			return Runtime{}, fmt.Errorf("destination %q proxy: %w", item.ID, err)
		}
		destinations = append(destinations, domain.Destination{
			ID:           item.ID,
			Name:         item.Name,
			Provider:     item.Provider,
			Endpoint:     item.Endpoint,
			Mount:        item.Mount,
			Remote:       item.Remote,
			Root:         item.Root,
			RcloneConfig: rcloneConfig,
			Token:        token,
			Proxy:        proxy,
		})
	}
	if defaultDestinationID != "" {
		if _, exists := seen[defaultDestinationID]; !exists {
			return Runtime{}, fmt.Errorf("default destination %q not found", defaultDestinationID)
		}
	}
	return Runtime{
		Config:               c,
		APIToken:             apiToken,
		Aria2Secret:          aria2Secret,
		DefaultDestinationID: defaultDestinationID,
		Destinations:         destinations,
	}, nil
}
