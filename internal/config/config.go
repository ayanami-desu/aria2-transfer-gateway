package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr  string      `yaml:"listen_addr"`
	DataFile    string      `yaml:"data_file"`
	DownloadDir string      `yaml:"download_dir"`
	WorkerCount int         `yaml:"worker_count"`
	CORSOrigins []string    `yaml:"cors_origins"`
	API         APIConfig   `yaml:"api"`
	Aria2       Aria2Config `yaml:"aria2"`
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

type Runtime struct {
	Config      Config
	APIToken    string
	Aria2Secret string
}

func Default() Config {
	return Config{
		ListenAddr:  "127.0.0.1:8787",
		DataFile:    "./data/tasks.db",
		DownloadDir: "./data/downloads",
		WorkerCount: 2,
		CORSOrigins: []string{"*"},
		API: APIConfig{
			TokenEnv: "GATEWAY_API_TOKEN",
		},
		Aria2: Aria2Config{
			Endpoint:  "http://127.0.0.1:6800/jsonrpc",
			SecretEnv: "ARIA2_RPC_SECRET",
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
	if c.API.TokenEnv == "" && c.API.Token == "" {
		c.API.TokenEnv = defaults.API.TokenEnv
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
	return Runtime{
		Config:      c,
		APIToken:    apiToken,
		Aria2Secret: aria2Secret,
	}, nil
}
