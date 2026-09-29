//go:build imagetools

package imagetools

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// The exporter's listener takes no credentials. These tests run the pinned
// exporter image with the environment the builder generates, in the network
// namespace of a Valkey container as in a data pod, under the rootless posture of
// docs/adr/0032-generated-pods-run-rootless.md, against both Valkey lines:
//
//   - /metrics answers with redis_up 1 (the positive control);
//   - /scrape naming a foreign target dials nothing, so the password reaches no
//     listener;
//   - /scrape with check-single-keys returns no key value;
//   - a key a CR author configures through spec.metrics.extraArgs is exported
//     with its size and without its value on /metrics.
//
// Each run carries its own negative control: the same image without the two
// variables the builder sets must send the password to the listener and return
// the value, or the assertions above could not fail.
//
// What this does not cover: a Kubernetes node, and a TLS target (the builder's
// TLS wiring is not set up here).

const (
	exporterPassword = "s3cret"
	exporterKey      = "customer:42:token"
	exporterValue    = "tok-abc-123"
	// controlListenPort is where the control exporter listens next to the
	// generated one, in the same network namespace.
	controlListenPort = 9122
)

// authFrame is what a client sends to authenticate with exporterPassword.
var authFrame = fmt.Sprintf("*2\r\n$4\r\nAUTH\r\n$%d\r\n%s\r\n", len(exporterPassword), exporterPassword)

// captureListener records every byte any client sends it. Each connection is
// read until the client stops sending for a moment, then closed, so a client
// waiting for a reply gets EOF instead of hanging on its timeout.
type captureListener struct {
	ln    net.Listener
	mu    sync.Mutex
	conns int
	data  strings.Builder
}

func newCaptureListener(t *testing.T) *captureListener {
	t.Helper()
	// Every interface: the containers reach it through the Docker host gateway.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	c := &captureListener{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf, _ := readUntilIdle(conn)
			c.mu.Lock()
			c.conns++
			c.data.Write(buf)
			c.mu.Unlock()
			_ = conn.Close()
		}
	}()
	return c
}

func readUntilIdle(conn net.Conn) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			return out, err
		}
	}
}

func (c *captureListener) port() int { return c.ln.Addr().(*net.TCPAddr).Port }

func (c *captureListener) received() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conns, c.data.String()
}

// generatedExporter returns the exporter container the builder renders for a
// cluster with auth and metrics, and the fixture key configured through
// extraArgs.
func generatedExporter(t *testing.T, valkeyImage string) corev1.Container {
	t.Helper()
	v := &vkov1.Valkey{Spec: vkov1.ValkeySpec{
		Replicas: 1,
		Image:    valkeyImage,
		Auth:     &vkov1.AuthSpec{SecretName: "auth", SecretPasswordKey: "password"},
		Metrics: &vkov1.MetricsSpec{
			Enabled:   true,
			ExtraArgs: []string{"--check-single-keys=db0=" + exporterKey},
		},
	}}
	for _, c := range builder.BuildStatefulSet(v, "operator:test").Spec.Template.Spec.Containers {
		if c.Name == builder.ExporterContainerName {
			return c
		}
	}
	t.Fatal("the builder rendered no exporter container")
	return corev1.Container{}
}

// dockerEnv renders env as docker -e arguments, the Secret reference resolved to
// exporterPassword. drop names variables to leave out; listen overrides the
// listen address when non-empty.
func dockerEnv(t *testing.T, env []corev1.EnvVar, listen string, drop ...string) []string {
	t.Helper()
	var args []string
	for _, e := range env {
		value := e.Value
		switch {
		case contains(drop, e.Name):
			continue
		case e.ValueFrom != nil:
			require.NotNil(t, e.ValueFrom.SecretKeyRef, "only a Secret reference is resolved here: %s", e.Name)
			value = exporterPassword
		case e.Name == "REDIS_EXPORTER_WEB_LISTEN_ADDRESS" && listen != "":
			value = listen
		}
		args = append(args, "-e", e.Name+"="+value)
	}
	return args
}

func contains(list []string, s string) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// startDetached runs a detached container and removes it when the test ends.
func startDetached(t *testing.T, args ...string) string {
	t.Helper()
	id, stderr, err := dockerRun(t, append([]string{"run", "-d", "--rm"}, args...)...)
	require.NoError(t, err, "docker run %v:\n%s", args, stderr)
	t.Cleanup(func() { _, _, _ = dockerRun(t, "rm", "-f", id) })
	return id
}

// publishedPort returns the host address docker published containerPort on.
func publishedPort(t *testing.T, id string, containerPort int) string {
	t.Helper()
	out, stderr, err := dockerRun(t, "port", id, fmt.Sprintf("%d/tcp", containerPort))
	require.NoError(t, err, stderr)
	return strings.TrimSpace(strings.Split(out, "\n")[0])
}

func httpGet(t *testing.T, base, path string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get("http://" + base + path)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// waitForRedisUp polls /metrics until the exporter reports the local Valkey up.
func waitForRedisUp(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	var last string
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + base + "/metrics")
		if err != nil {
			last = err.Error()
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		last = string(body)
		return strings.Contains(last, "\nredis_up 1\n")
	}, 60*time.Second, 500*time.Millisecond, "exporter at %s never reported redis_up 1:\n%.2000s", base, last)
}

func scrapeForeign(t *testing.T, base string, ln *captureListener) {
	t.Helper()
	target := fmt.Sprintf("redis://host.docker.internal:%d", ln.port())
	_, _ = httpGet(t, base, "/scrape?target="+url.QueryEscape(target))
}

func scrapeKey(t *testing.T, base string) string {
	t.Helper()
	q := url.Values{"target": {"redis://localhost:6379"}, "check-single-keys": {"db0=" + exporterKey}}
	_, body := httpGet(t, base, "/scrape?"+q.Encode())
	return body
}

func TestExporter_ServesNoScrapeRoute(t *testing.T) {
	t.Parallel()
	for name, image := range pinnedImages() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exporter := generatedExporter(t, image)

			// The Valkey container owns the network namespace, so it publishes the
			// exporters' ports and carries the host gateway name they dial.
			valkey := startDetached(t,
				"--add-host=host.docker.internal:host-gateway",
				"-p", fmt.Sprintf("127.0.0.1::%d", vkov1.DefaultMetricsExporterPort),
				"-p", fmt.Sprintf("127.0.0.1::%d", controlListenPort),
				image, "valkey-server", "--requirepass", exporterPassword, "--save", "", "--appendonly", "no")
			require.Eventually(t, func() bool {
				out, _, err := dockerRun(t, "exec", valkey, "valkey-cli", "-a", exporterPassword, "--no-auth-warning",
					"SET", exporterKey, exporterValue)
				return err == nil && out == "OK"
			}, 60*time.Second, 500*time.Millisecond, "valkey never accepted the fixture key")

			exporterArgs := func(env []string) []string {
				args := append([]string{"--network", "container:" + valkey}, restrictedFlags...)
				args = append(args, env...)
				args = append(args, exporter.Image)
				return append(args, exporter.Args...)
			}
			startDetached(t, exporterArgs(dockerEnv(t, exporter.Env, ""))...)
			startDetached(t, exporterArgs(dockerEnv(t, exporter.Env, fmt.Sprintf(":%d", controlListenPort),
				"REDIS_EXPORTER_DISABLE_SCRAPE_ENDPOINT", "REDIS_EXPORTER_DISABLE_EXPORTING_KEY_VALUES"))...)

			generated := publishedPort(t, valkey, int(vkov1.DefaultMetricsExporterPort))
			control := publishedPort(t, valkey, controlListenPort)
			waitForRedisUp(t, generated)
			waitForRedisUp(t, control)

			// Negative control first: without the two variables the route leaks.
			controlListener := newCaptureListener(t)
			scrapeForeign(t, control, controlListener)
			conns, got := controlListener.received()
			require.Equal(t, 1, conns, "the control exporter must dial the foreign target")
			require.Contains(t, got, authFrame, "the control exporter must send the password to it")
			require.Contains(t, scrapeKey(t, control), `val="`+exporterValue+`"`,
				"the control exporter must return the key value")
			_, controlMetrics := httpGet(t, control, "/metrics")
			require.Contains(t, controlMetrics, `val="`+exporterValue+`"`,
				"the control exporter must export the configured key's value")

			// The generated container.
			ln := newCaptureListener(t)
			scrapeForeign(t, generated, ln)
			conns, got = ln.received()
			assert.Zero(t, conns, "the exporter dialled a target named in the request")
			assert.NotContains(t, got, exporterPassword)

			body := scrapeKey(t, generated)
			assert.False(t, strings.Contains(body, "redis_key_value_as_string") || strings.Contains(body, exporterValue),
				"/scrape returned the key value")
			_, metrics := httpGet(t, generated, "/metrics")
			assert.Contains(t, metrics, `redis_key_size{db="db0",key="`+exporterKey+`"}`,
				"the configured key is checked, so the missing value below is not vacuous")
			assert.False(t, strings.Contains(metrics, exporterValue), "/metrics exports the key value")
		})
	}
}
