//go:build integration

// The real Sigstore chain, over the network (opt-in: `go test -tags integration`). crowsnest has
// no signed release yet, so this uses a real one made the same way: cosign's own release signs
// cosign_checksums.txt keylessly with cosign v3, the version the release workflow installs, so
// its bundle has the format crowsnest's will (drillbit's older releases carry the legacy cosign
// bundle, which this does not accept). Its signer is a Google service account rather than a
// GitHub workflow; everything else (TUF root, Fulcio chain, SCT, Rekor entry, timestamps) is the
// same path. The bundle must verify for cosign's identity, be refused under crowsnest's, and
// fail on a changed artifact.

package update

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationSigstoreVerifiesARealRelease(t *testing.T) {
	const base = "https://github.com/sigstore/cosign/releases/download/v3.1.5/"
	client := &Client{HTTP: &http.Client{}, UserAgent: "crowsnest-test"}
	checksums, err := client.download(context.Background(), base+"cosign_checksums.txt")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := client.download(context.Background(), base+"cosign_checksums.txt.sigstore.json")
	if err != nil {
		t.Fatal(err)
	}
	const issuer, signer = "https://accounts.google.com", "^keyless@projectsigstore\\.iam\\.gserviceaccount\\.com$"
	if err := verifySigstore(checksums, bundle, issuer, signer); err != nil {
		t.Fatalf("a real signed release did not verify: %v", err)
	}
	if err := SigstoreVerifier(checksums, bundle); err == nil {
		t.Fatal("another repository's signature was accepted as crowsnest's")
	}
	tampered := []byte(strings.Replace(string(checksums), "a", "b", 1))
	if err := verifySigstore(tampered, bundle, issuer, signer); err == nil {
		t.Fatal("a tampered checksums.txt verified")
	}
}
