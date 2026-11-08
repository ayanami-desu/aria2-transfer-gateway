package ed2k

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

func TestUpdaterRefreshAppliesGlobalOption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.2.3.4:4661\n5.6.7.8:4725"))
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := &recordingGlobalOptions{}
	source := NewSource(server.URL, server.Client())
	updater := NewUpdater(source, client, 0, log.New(&logs, "", 0))
	if err := updater.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.options, map[string]string{"ed2k-server": "1.2.3.4:4661,5.6.7.8:4725"}) {
		t.Fatalf("global options = %#v", client.options)
	}
	if logs.String() != "ed2k server list updated: 2 servers from "+server.URL+"\n" {
		t.Fatalf("logs = %q", logs.String())
	}
}

func TestUpdaterRefreshSkipsUnchangedList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.2.3.4:4661"))
	}))
	defer server.Close()
	client := &recordingGlobalOptions{}
	updater := NewUpdater(NewSource(server.URL, server.Client()), client, time.Hour, log.Default())
	if err := updater.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.calls = 0
	if err := updater.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.calls != 0 {
		t.Fatalf("global option calls = %d, want 0", client.calls)
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
	urls    []string
	options map[string]any
}

func (d *recordingDownloader) AddURI(_ context.Context, urls []string, _ string, _ bool, options map[string]any) (string, error) {
	d.urls = urls
	d.options = options
	return "gid", nil
}

type recordingGlobalOptions struct {
	options map[string]string
	calls   int
}

func (c *recordingGlobalOptions) ChangeGlobalOption(_ context.Context, options map[string]string) error {
	c.options = options
	c.calls++
	return nil
}
