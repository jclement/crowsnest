package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/jclement/crowsnest/internal/update"
)

// fakeGitHub answers releases/latest with a signed-looking v1.2.3 for this platform.
func fakeGitHub(t *testing.T) *update.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"tag_name":"v1.2.3","html_url":"https://example.invalid/v1.2.3","assets":[
			{"name":"crowsnest_1.2.3_%s_%s.tar.gz","browser_download_url":"https://example.invalid/a"},
			{"name":"checksums.txt","browser_download_url":"https://example.invalid/c"},
			{"name":"checksums.txt.sigstore.json","browser_download_url":"https://example.invalid/b"}]}`,
			runtime.GOOS, runtime.GOARCH)
	}))
	t.Cleanup(server.Close)
	return &update.Client{API: server.URL, HTTP: server.Client(), UserAgent: "test",
		Verify: func(_, _ []byte, _ string) error { return errors.New("tests never install") }}
}

func TestUpdateCheck(t *testing.T) {
	if update.InContainer() {
		t.Skip("self-update is refused inside a container")
	}
	client := fakeGitHub(t)
	var out bytes.Buffer
	if err := runUpdate(context.Background(), &out, client, "v1.0.0", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "crowsnest v1.2.3 is available (running v1.0.0)") {
		t.Fatalf("output %q", out.String())
	}
	out.Reset()
	if err := runUpdate(context.Background(), &out, client, "v1.2.3", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "v1.2.3 is the latest release") {
		t.Fatalf("output %q", out.String())
	}
}

func TestUpdateRefusesADevBuild(t *testing.T) {
	client := fakeGitHub(t)
	for _, checkOnly := range []bool{true, false} {
		err := runUpdate(context.Background(), &bytes.Buffer{}, client, "dev", checkOnly)
		var refusal *update.Refusal
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "development build") {
			t.Fatalf("check=%v: want the dev-build refusal, got %v", checkOnly, err)
		}
	}
}
