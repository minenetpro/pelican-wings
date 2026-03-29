package docker

import "testing"

func TestImageReferenceExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		refs []string
		want bool
	}{
		{
			name: "matches repo tag",
			ref:  "ghcr.io/pelican-dev/example:latest",
			refs: []string{"ghcr.io/pelican-dev/example:latest"},
			want: true,
		},
		{
			name: "matches repo digest",
			ref:  "ghcr.io/pelican-dev/example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			refs: []string{"ghcr.io/pelican-dev/example@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			want: true,
		},
		{
			name: "returns false when missing",
			ref:  "ghcr.io/pelican-dev/example:latest",
			refs: []string{"ghcr.io/pelican-dev/example:stable"},
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := imageReferenceExists(tt.ref, tt.refs); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
