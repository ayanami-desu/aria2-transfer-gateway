package ed2k

import (
	"context"
	"reflect"
	"testing"
)

func TestDownloaderInjectsServersForED2K(t *testing.T) {
	source := &Source{list: []string{"5.6.7.8:4725", "1.2.3.4:4661"}}
	base := &recordingDownloader{}
	downloader := NewDownloader(base, source)
	options := map[string]any{"ed2k-server": " 9.9.9.9:4661 , 5.6.7.8:4725 ", "out": "file"}
	if _, err := downloader.AddURI(context.Background(), []string{"ed2k://|file|name|1|0123456789abcdef0123456789abcdef|/"}, "/downloads", false, options); err != nil {
		t.Fatal(err)
	}
	if options["ed2k-server"] != " 9.9.9.9:4661 , 5.6.7.8:4725 " {
		t.Fatalf("caller options mutated: %#v", options)
	}
	want := "9.9.9.9:4661,5.6.7.8:4725,1.2.3.4:4661"
	if got := base.options["ed2k-server"]; got != want {
		t.Fatalf("merged ed2k-server = %v, want %v", got, want)
	}
	if base.options["out"] != "file" {
		t.Fatalf("other options lost: %#v", base.options)
	}
}

func TestDownloaderLeavesNonED2KRequestsAlone(t *testing.T) {
	source := &Source{list: []string{"1.2.3.4:4661"}}
	base := &recordingDownloader{}
	downloader := NewDownloader(base, source)
	options := map[string]any{"out": "file"}
	if _, err := downloader.AddURI(context.Background(), []string{"https://example.test/file"}, "/downloads", false, options); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.options, options) {
		t.Fatalf("options = %#v, want %#v", base.options, options)
	}
}

func TestHasED2KURL(t *testing.T) {
	if !hasED2KURL([]string{"https://example.test", "ED2K://|file|x|1|0123456789abcdef0123456789abcdef|/"}) {
		t.Fatal("hasED2KURL() = false, want true")
	}
	if hasED2KURL([]string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}) {
		t.Fatal("hasED2KURL(magnet) = true, want false")
	}
}
