package main

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type updateTestTransport func(*http.Request) (*http.Response, error)

func (f updateTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReleaseVersionOrder(t *testing.T) {
	for _, test := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v1.20.6", true},
		{"v1.0.9", "v1.0.10", false},
		{"v1.0.5", "v1.0.5", false},
		{"v1.0.6-beta.1", "v1.0.5", false},
		{"v1.0.5", "dev", false},
	} {
		if got := newerRelease(test.latest, test.current); got != test.want {
			t.Errorf("newerRelease(%q, %q) = %v, want %v", test.latest, test.current, got, test.want)
		}
	}
}

func TestReleaseDownloadRejectsInvalidChecksum(t *testing.T) {
	state := t.TempDir()
	u := newReleaseUpdater(filepath.Join(state, "filemanager"), state)
	u.client.Transport = updateTestTransport(func(r *http.Request) (*http.Response, error) {
		body := "invalid archive"
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
			body = strings.Repeat("0", 64) + "  file-manager-linux-" + runtime.GOARCH + ".tar.gz\n"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if file, err := u.download(context.Background(), "v1.0.6"); file != "" || err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("download returned %q, %v", file, err)
	}
}
