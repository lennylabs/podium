package chart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/buildinfo"
)

// releaseImage is the repository the release pipeline pushes the registry
// image to (.github/workflows/release.yml).
const releaseImage = "ghcr.io/lennylabs/podium-server"

// The chart ships with the release it is tagged in, so its version and
// appVersion equal the build's version. A chart left at another version
// renders an image tag the release never published, and the install sits in
// ImagePullBackOff.
func TestChart_VersionTracksTheRelease(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(chartDir, "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	var chart struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(raw, &chart); err != nil {
		t.Fatalf("parse Chart.yaml: %v", err)
	}
	if chart.Version != buildinfo.Version || chart.AppVersion != buildinfo.Version {
		t.Errorf("Chart.yaml version=%q appVersion=%q; both must equal buildinfo.Version %q",
			chart.Version, chart.AppVersion, buildinfo.Version)
	}
}

// With no image override, the registry container and the migrate Job run the
// image the release publishes, at the bare version tag the release pushes.
func TestChart_DefaultImageIsThePublishedImage(t *testing.T) {
	out := render(t, withSigningKey)
	want := `image: "` + releaseImage + ":" + buildinfo.Version + `"`
	if !strings.Contains(out, want) {
		t.Errorf("default render does not carry %s", want)
	}
	if strings.Contains(out, "ghcr.io/lennylabs/podium:") {
		t.Errorf("default render names ghcr.io/lennylabs/podium, which no release publishes")
	}
}
