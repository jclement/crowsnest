// The Sigstore half of self-update: proving checksums.txt was signed by this repository's
// release workflow. Kept apart from update.go so the trust policy is in one short place.

package update

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// githubOIDCIssuer issues the tokens keyless cosign signs with inside GitHub Actions.
const githubOIDCIssuer = "https://token.actions.githubusercontent.com"

// signerIdentity is the certificate SAN keyless cosign gets in .github/workflows/release.yml on
// a v* tag: the workflow file at the tag's ref. Anything else (another repository, another
// workflow such as a pull request's CI, a branch) is refused. Exported through SignerPattern so
// the README's cosign example and this stay the same.
var signerIdentity = "^https://github\\.com/" + regexp.QuoteMeta(Owner+"/"+Repo) +
	"/\\.github/workflows/release\\.yml@refs/tags/v"

// SignerPattern is the certificate identity regexp a release signature must match.
func SignerPattern() string { return signerIdentity }

// SigstoreVerifier verifies a Sigstore bundle (cosign sign-blob --bundle, the protobuf bundle
// format) over artifact against the public-good instance: the TUF trusted root is fetched and
// cached by sigstore-go, then the Fulcio certificate chain, its SCT, the Rekor entry and the
// signer identity are checked.
func SigstoreVerifier(artifact, bundleJSON []byte) error {
	return verifySigstore(artifact, bundleJSON, githubOIDCIssuer, signerIdentity)
}

// verifySigstore is SigstoreVerifier with the signer's OIDC issuer and identity as parameters,
// so the opt-in network test can check the real chain against another project's real release.
func verifySigstore(artifact, bundleJSON []byte, issuer, identityPattern string) error {
	var signed bundle.Bundle
	if err := signed.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("reading the Sigstore bundle: %w", err)
	}
	trusted, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return fmt.Errorf("loading the Sigstore trusted root: %w", err)
	}
	verifier, err := verify.NewVerifier(trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return fmt.Errorf("creating the Sigstore verifier: %w", err)
	}
	identity, err := verify.NewShortCertificateIdentity(issuer, "", "", identityPattern)
	if err != nil {
		return fmt.Errorf("building the expected signer identity: %w", err)
	}
	_, err = verifier.Verify(&signed, verify.NewPolicy(
		verify.WithArtifact(bytes.NewReader(artifact)),
		verify.WithCertificateIdentity(identity),
	))
	return err
}
