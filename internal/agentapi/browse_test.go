// The folder picker is the owner's browser's alone (DESIGN.md 4.23): the agent socket has no
// /browse routes, whatever token the caller holds.

package agentapi

import (
	"context"
	"net/http"
	"testing"
)

func TestAgentSocketHasNoBrowseRoutes(t *testing.T) {
	f := startSocket(t)
	client := unixHTTP(f.socket)
	for _, path := range []string{"/api/v1/browse?path=/", "/api/v1/browse/facts?path=/", "/api/v1/browse/places"} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://crowsnest"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+f.token(t, "w1:p1", ""))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s on the agent socket: %d, want 404", path, response.StatusCode)
		}
	}
}
