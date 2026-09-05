package releasetest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release mirror URL and the archive name are each spelled out independently in the build config,
// the two installers, the release script, the workflow and the Go self-updater. Nothing at build time
// ties them together: the mirror path is only exercised when GitHub is unreachable, so a half-finished
// rename stays green until the day it matters. These two tests are that tie.

// mirrorURL is the one true https mirror, taken from .goreleaser.yaml (the ldflag baked into every
// release binary). Every other spelling is asserted equal to it.
func mirrorURL(t *testing.T) string {
	t.Helper()
	return capture(t, ".goreleaser.yaml", `internal/cli\.defaultMirror=(\S+)`)
}

func TestReleaseMirrorSpelledTheSameEverywhere(t *testing.T) {
	want := mirrorURL(t)
	if !strings.HasPrefix(want, "https://") {
		t.Fatalf("goreleaser mirror ldflag = %q, want an https URL", want)
	}

	for _, tc := range []struct{ file, pattern string }{
		{"scripts/cloud-setup.sh", `RC_MIRROR="\$\{RC_RELEASE_MIRROR:-([^}]+)\}"`},
		{"scripts/release.sh", `RELEASE_MIRROR_URL="\$\{RC_RELEASE_MIRROR:-([^}]+)\}"`},
	} {
		if got := capture(t, tc.file, tc.pattern); got != want {
			t.Errorf("%s mirror default = %q, want %q (.goreleaser.yaml)", tc.file, got, want)
		}
	}

	// The workflow writes the same location in the s3:// dialect: bucket = the virtual-hosted
	// subdomain, key prefix = the URL path.
	host, prefix, ok := strings.Cut(strings.TrimPrefix(want, "https://"), "/")
	bucket, _, hasSuffix := strings.Cut(host, ".s3.")
	if !ok || !hasSuffix {
		t.Fatalf("mirror URL %q is not a virtual-hosted S3 URL", want)
	}
	wantS3 := fmt.Sprintf("s3://%s/%s", bucket, prefix)
	if got := capture(t, ".github/workflows/release.yml", `S3_MIRROR:\s*(\S+)`); got != wantS3 {
		t.Errorf("release.yml S3_MIRROR = %q, want %q (from %s)", got, wantS3, want)
	}
}

// TestReleaseAssetNameSpelledTheSameEverywhere asserts every dialect builds the same archive name.
// Each template is reduced to its skeleton (see normalizeAssetTemplate), so only the shape
// rc_<ver>_<os>_<arch> and the extension are compared — the substitution syntax, and whether a caller
// hard-codes its own platform, are none of this test's business.
func TestReleaseAssetNameSpelledTheSameEverywhere(t *testing.T) {
	for _, tc := range []struct{ file, pattern, want string }{
		{".goreleaser.yaml", `name_template:\s*"([^"]+)"`, "rc_%s_%s_%s"},                  // extension comes from `formats`
		{"internal/cli/upgrade.go", `return fmt\.Sprintf\("(rc_[^"]+)"`, "rc_%s_%s_%s.%s"}, // .zip on windows
		{"scripts/cloud-setup.sh", `asset="(rc_[^"]+)"`, "rc_%s_%s_%s.tar.gz"},
		{"scripts/install.sh", `asset="(rc_[^"]+)"`, "rc_%s_%s_%s.tar.gz"},
		{".github/workflows/release.yml", `--pattern "(rc_[^"]+)"`, "rc_%s_%s_%s.tar.gz"},
	} {
		if got := normalizeAssetTemplate(capture(t, tc.file, tc.pattern)); got != tc.want {
			t.Errorf("%s asset template = %q, want %q", tc.file, got, tc.want)
		}
	}
}

// placeholders matches every substitution dialect in play: goreleaser `{{ .Version }}`, shell
// `${version}` / `${rc_tag#v}`, and Go's own `%s`.
var placeholders = regexp.MustCompile(`\{\{[^}]*\}\}|\$\{[^}]*\}|%s`)

// normalizeAssetTemplate reduces one archive-name template to its skeleton: every underscore-separated
// field after the "rc" prefix becomes %s, whatever produced it (goreleaser `{{ .Os }}`, shell
// `${version}`, Go `%s`, or a literal "linux"). The extension is kept verbatim — it is the one part
// the dialects legitimately differ on.
func normalizeAssetTemplate(tpl string) string {
	head, ext, hasExt := strings.Cut(placeholders.ReplaceAllString(tpl, "%s"), ".")
	fields := strings.Split(head, "_")
	for i := 1; i < len(fields); i++ {
		fields[i] = "%s"
	}
	head = strings.Join(fields, "_")
	if hasExt {
		return head + "." + ext
	}
	return head
}

// capture returns the first submatch of pattern in the repo file, failing the test if it is absent —
// an absent match means the constant moved and this guard silently stopped guarding it.
func capture(t *testing.T, file, pattern string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", file))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(pattern).FindSubmatch(body)
	if m == nil {
		t.Fatalf("%s: no match for %s — the constant moved; update this guard", file, pattern)
	}
	return string(m[1])
}
