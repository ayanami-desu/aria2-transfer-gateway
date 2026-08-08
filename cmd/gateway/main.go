package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aria2-transfer-gateway/internal/aria2"
	"aria2-transfer-gateway/internal/config"
	"aria2-transfer-gateway/internal/httpapi"
	"aria2-transfer-gateway/internal/provider"
	"aria2-transfer-gateway/internal/store"
	"aria2-transfer-gateway/internal/transfer"
)

func main() {
	configPath := flag.String("config", "config.yaml", "gateway configuration file")
	addrOverride := flag.String("addr", "", "override HTTP listen address")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := cfg.Resolve()
	if err != nil {
		log.Fatal(err)
	}
	if *addrOverride != "" {
		runtime.Config.ListenAddr = *addrOverride
	}
	taskStore, err := store.Open(runtime.Config.DataFile)
	if err != nil {
		log.Fatal(err)
	}
	defer taskStore.Close()
	downloader := aria2.NewClient(runtime.Config.Aria2.Endpoint, runtime.Aria2Secret, nil)
	configureAria2Hooks(downloader, runtime.Config.Aria2)
	providers := map[string]provider.Provider{
		"rclone":   provider.NewRclone(""),
		"openlist": provider.NewOpenList(nil),
	}
	service, err := transfer.NewService(
		taskStore,
		downloader,
		providers,
		nil,
		"",
		runtime.Config.DownloadDir,
		runtime.Config.WorkerCount,
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service.Start(ctx)

	api := httpapi.NewServer(service, runtime.APIToken, runtime.Config.CORSOrigins)
	server := &http.Server{
		Addr:              runtime.Config.ListenAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	log.Printf("aria2 transfer gateway listening on %s", runtime.Config.ListenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func configureAria2Hooks(client *aria2.Client, cfg config.Aria2Config) {
	options := make(map[string]string, 2)
	if cfg.CompleteHook != "" {
		options["on-download-complete"] = cfg.CompleteHook
	}
	if cfg.StoppedHook != "" {
		options["on-download-stop"] = cfg.StoppedHook
	}
	if len(options) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt < 15; attempt++ {
		if err = client.ChangeGlobalOption(ctx, options); err == nil {
			log.Printf("aria2 hooks configured")
			return
		}
		select {
		case <-ctx.Done():
			log.Printf("warning: unable to configure aria2 hooks: %v", ctx.Err())
			return
		case <-time.After(2 * time.Second):
		}
	}
	log.Printf("warning: unable to configure aria2 hooks: %v", err)
}
