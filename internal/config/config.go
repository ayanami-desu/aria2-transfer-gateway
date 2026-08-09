package config

import (
	"fmt"
	"os"

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
			GatewaySecret: "GATEWAY_API_TOKEN",
		},
		Aria2: Aria2Config{
			Endpoint:      "http://127.0.0.1:6800/jsonrpc",
			AriaRPCSecret: "ARIA2_RPC_SECRET",
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
}

func (c Config) Resolve() (Runtime, error) {
	apiToken := c.API.Token
	if apiToken == "" && c.API.GatewaySecret != "" {
		apiToken = os.Getenv(c.API.GatewaySecret)
	}
	aria2Secret := c.Aria2.Secret
	if aria2Secret == "" && c.Aria2.AriaRPCSecret != "" {
		aria2Secret = os.Getenv(c.Aria2.AriaRPCSecret)
	}
	return Runtime{
		Config:      c,
		APIToken:    apiToken,
		Aria2Secret: aria2Secret,
	}, nil
}
