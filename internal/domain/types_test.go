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
