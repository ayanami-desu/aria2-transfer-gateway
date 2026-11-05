package tracker

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"aria2-transfer-gateway/internal/aria2"
)

func TestParseList(t *testing.T) {
	got := ParseList("\ufeff# comment\n https://one.example/a,udp://two.example:80\nhttps://one.example/a\nftp://bad.example\n")
	want := []string{"https://one.example/a", "udp://two.example:80"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseList() = %#v, want %#v", got, want)
	}
}

func TestDownloaderMergesTrackersWithoutMutatingOptions(t *testing.T) {
	source := &Source{list: []string{"https://cached.example", "https://user.example"}}
	base := &recordingDownloader{}
	downloader := NewDownloader(base, source)
	options := map[string]any{"bt-tracker": " https://user.example,https://existing.example ", "out": "file"}
	if _, err := downloader.AddTorrent(context.Background(), "torrent", "/downloads", false, options); err != nil {
		t.Fatal(err)
	}
	if options["bt-tracker"] != " https://user.example,https://existing.example " {
		t.Fatalf("caller options mutated: %#v", options)
	}
	if got, want := base.options["bt-tracker"], "https://user.example,https://existing.example,https://cached.example"; got != want {
		t.Fatalf("merged bt-tracker = %v, want %v", got, want)
	}
}

func TestSourceRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("https://one.example\n# ignored\nudp://two.example:80"))
	}))
	defer server.Close()
	source := NewSource(server.URL, server.Client())
	count, err := source.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || !reflect.DeepEqual(source.List(), []string{"https://one.example", "udp://two.example:80"}) {
		t.Fatalf("refresh = %d, %#v", count, source.List())
	}
}

func TestUpdaterRefreshAppliesGlobalOption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("https://one.example\nhttps://two.example"))
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := &recordingGlobalOptions{}
	source := NewSource(server.URL, server.Client())
	updater := NewUpdater(source, client, 0, log.New(&logs, "", 0))
	if err := updater.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.options, map[string]string{"bt-tracker": "https://one.example,https://two.example"}) {
		t.Fatalf("global options = %#v", client.options)
	}
	if logs.String() != "tracker list updated: 2 trackers from "+server.URL+"\n" {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestNextRetryDelay(t *testing.T) {
	if got := nextRetryDelay(time.Minute); got != 2*time.Minute {
		t.Fatalf("nextRetryDelay(1m) = %s, want 2m", got)
	}
	if got := nextRetryDelay(32 * time.Minute); got != time.Hour {
		t.Fatalf("nextRetryDelay(32m) = %s, want 1h", got)
	}
	if got := nextRetryDelay(time.Hour); got != time.Hour {
		t.Fatalf("nextRetryDelay(1h) = %s, want 1h", got)
	}
}

type recordingDownloader struct {
	aria2.Downloader
	options map[string]any
}

func (d *recordingDownloader) AddTorrent(_ context.Context, _ string, _ string, _ bool, options map[string]any) (string, error) {
	d.options = options
	return "gid", nil
}

func (d *recordingDownloader) AddURI(_ context.Context, _ []string, _ string, _ bool, options map[string]any) (string, error) {
	d.options = options
	return "gid", nil
}

type recordingGlobalOptions struct {
	options map[string]string
}

func (c *recordingGlobalOptions) ChangeGlobalOption(_ context.Context, options map[string]string) error {
	c.options = options
	return nil
}
