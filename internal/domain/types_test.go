package domain

import "testing"

func TestNormalizeTargetPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty root", input: "", want: "/"},
		{name: "nested path", input: "/movies/2026", want: "/movies/2026"},
		{name: "leading whitespace", input: "  /movies  ", want: "/movies"},
		{name: "parent escape", input: "/movies/../private", wantErr: true},
		{name: "backslash escape", input: `..\\private`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeTargetPath(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got path %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeTargetPath() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("NormalizeTargetPath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizeProxyURL(t *testing.T) {
	tests := []struct {
		value   string
		want    string
		wantErr bool
	}{
		{value: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{value: "HTTPS://proxy.example:8443/", want: "https://proxy.example:8443"},
		{value: "socks5://user:password@proxy.example:1080", want: "socks5://user:password@proxy.example:1080"},
		{value: "ftp://proxy.example:21", wantErr: true},
		{value: "http:///missing-host", wantErr: true},
		{value: "http://proxy.example/path", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got, err := NormalizeProxyURL(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeProxyURL() = %q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("NormalizeProxyURL() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestTaskViewIncludesMagnetDisplayNameBeforeCompletion(t *testing.T) {
	task := Task{URLs: []string{"magnet:?xt=urn:btih:test&dn=Exact%20Name"}}

	view := task.View("")
	if len(view.FileNames) != 1 || view.FileNames[0] != "Exact Name" {
		t.Fatalf("task file names = %#v, want [Exact Name]", view.FileNames)
	}
}
