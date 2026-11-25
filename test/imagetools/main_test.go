//go:build imagetools

package imagetools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// pullAttempts and pullBackoff bound the pre-pull: five attempts, waiting 5, 10, 20
// and 40 s between them, so a registry outage of a little over a minute is ridden
// out and a lasting one still fails the job within minutes.
const (
	pullAttempts = 5
	pullBackoff  = 5 * time.Second
)

// TestMain pulls every image the checks run before any check starts, retrying a
// failed pull. Docker Hub answers a pull with a token request to auth.docker.io
// first, and that request failed on 2026-11-24 with a TLS handshake timeout on a
// runner that was logged in: one transient network error turned the job red and
// skipped the release (docs/adr/0017-test-and-ci-policy.md, D61). Once an image is
// local, `docker run` (pull policy "missing") does not contact the registry again,
// so the checks themselves need no retry. The image list is read from where the
// pins live, never copied (D43).
func TestMain(m *testing.M) {
	if err := prePull(imagesUnderTest()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// imagesUnderTest returns every image a check runs, each once, in a stable order.
func imagesUnderTest() []string {
	seen := map[string]bool{vkov1.DefaultMetricsExporterImage: true}
	for _, image := range pinnedImages() {
		seen[image] = true
	}
	images := make([]string, 0, len(seen))
	for image := range seen {
		images = append(images, image)
	}
	sort.Strings(images)
	return images
}

func prePull(images []string) error {
	for _, image := range images {
		if err := pullWithRetry(image); err != nil {
			return err
		}
	}
	return nil
}

// pullWithRetry pulls image, retrying with doubling backoff. A reference the
// registry does not know is not retried: no wait makes a wrong pin right.
func pullWithRetry(image string) error {
	delay := pullBackoff
	var lastErr error
	for attempt := 1; attempt <= pullAttempts; attempt++ {
		stderr, err := dockerPull(image)
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("docker pull %s (attempt %d/%d): %w\n%s", image, attempt, pullAttempts, err, stderr)
		if permanentPullFailure(stderr) || attempt == pullAttempts {
			break
		}
		fmt.Fprintf(os.Stderr, "%v\nretrying in %s\n", lastErr, delay)
		time.Sleep(delay)
		delay *= 2
	}
	return lastErr
}

func dockerPull(image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), imageProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "pull", "--quiet", image)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

// permanentPullFailure reports the answers no retry changes: the tag or digest
// does not exist, or the repository does not.
func permanentPullFailure(stderr string) bool {
	for _, marker := range []string{"manifest unknown", "not found", "repository does not exist"} {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
}
