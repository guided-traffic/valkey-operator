package health

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// ---------------------------------------------------------------------------
// Harness
//
// Checker has no client-factory seam: newValkeyClient is a plain method, unlike
// ValkeyReconciler.NewValkeyClientFn. Every connection therefore goes out to the
// real network stack against pod FQDNs that do not exist, so the observable
// behaviour of CheckCluster / PingPod / GetReplicationInfo / findMaster /
// checkSentinel is limited to "which address did it dial, and how did it report
// the failure". Two things are needed to observe that cheaply:
//
//  1. Lookups of *.svc.cluster.local names are answered by the host resolver.
//     On macOS the .local TLD is routed to mDNS, which costs a 5 second timeout
//     per pod. resolverProbe replaces the process resolver with one that fails
//     instantly and records the queried name, which keeps the package hermetic
//     (no network at all) and turns 5s per pod into microseconds.
//  2. checkSentinel returns only a bool, so the addresses it probed are visible
//     only through the queried names and its V(1) log lines.
// ---------------------------------------------------------------------------

// resolverProbe captures every DNS question the test binary asks for.
var resolverProbe = &dnsProbe{}

func TestMain(m *testing.M) {
	// Suppress the controller-runtime "SetLogger was never called" warning; the
	// tests that care about log output inject their own logger via context.
	ctrllog.SetLogger(logr.Discard())

	net.DefaultResolver.PreferGo = true
	net.DefaultResolver.Dial = resolverProbe.dial

	os.Exit(m.Run())
}

// clusterDomainSuffix is the suffix every pod address ends in.
const clusterDomainSuffix = ".svc.cluster.local"

type dnsProbe struct {
	mu    sync.Mutex
	names map[string]int
}

// dial hands the Go resolver a connection that records the query and then fails
// the exchange, so no lookup ever leaves the process.
func (p *dnsProbe) dial(_ context.Context, _, _ string) (net.Conn, error) {
	return &dnsProbeConn{probe: p}, nil
}

func (p *dnsProbe) record(query []byte) {
	name := dnsQuestionName(query)
	if name == "" {
		return
	}
	// A failed lookup is retried with the machine's search domains appended, so
	// one dial produces several queries for the same pod. Normalise back to the
	// cluster-internal name the checker actually asked for.
	if i := strings.Index(name, clusterDomainSuffix); i >= 0 {
		name = name[:i+len(clusterDomainSuffix)]
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.names == nil {
		p.names = map[string]int{}
	}
	p.names[name]++
}

func (p *dnsProbe) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = map[string]int{}
}

// hosts returns the distinct hostnames that were looked up, sorted.
func (p *dnsProbe) hosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.names))
	for name := range p.names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// dnsProbeConn is the net.Conn handed to the Go resolver. Reads fail
// immediately, which aborts the exchange without any timeout.
type dnsProbeConn struct {
	probe *dnsProbe
}

func (c *dnsProbeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *dnsProbeConn) Write(b []byte) (int, error)      { c.probe.record(b); return len(b), nil }
func (c *dnsProbeConn) Close() error                     { return nil }
func (c *dnsProbeConn) LocalAddr() net.Addr              { return &net.UDPAddr{IP: net.IPv4zero} }
func (c *dnsProbeConn) RemoteAddr() net.Addr             { return &net.UDPAddr{IP: net.IPv4zero} }
func (c *dnsProbeConn) SetDeadline(time.Time) error      { return nil }
func (c *dnsProbeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *dnsProbeConn) SetWriteDeadline(time.Time) error { return nil }

// dnsQuestionName extracts the QNAME from a DNS query. dnsProbeConn does not
// implement net.PacketConn, so the resolver frames the message like a stream
// connection and the 12 byte header starts at offset 2. The unframed layout is
// tried as a fallback; if neither decodes, the name is dropped and the
// assertions in TestDNSProbe_RecordsTheQueriedHostname fail loudly.
func dnsQuestionName(query []byte) string {
	if name := dnsQuestionNameAt(query, 14); name != "" {
		return name
	}
	return dnsQuestionNameAt(query, 12)
}

func dnsQuestionNameAt(query []byte, offset int) string {
	var labels []string
	for i := offset; i < len(query); {
		n := int(query[i])
		i++
		if n == 0 {
			break
		}
		if n > 63 || i+n > len(query) {
			return ""
		}
		label := query[i : i+n]
		if !isDNSLabel(label) {
			return ""
		}
		labels = append(labels, string(label))
		i += n
	}
	return strings.Join(labels, ".")
}

func isDNSLabel(label []byte) bool {
	for _, b := range label {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-', b == '_':
		default:
			return false
		}
	}
	return len(label) > 0
}

// logCapture collects the log lines a Checker emits through the context logger.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (c *logCapture) intoContext(ctx context.Context) context.Context {
	logger := funcr.New(func(prefix, args string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.lines = append(c.lines, prefix+" "+args)
	}, funcr.Options{Verbosity: 2})
	return ctrllog.IntoContext(ctx, logger)
}

func (c *logCapture) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// newProbeContext resets the DNS probe and returns a context carrying a log
// capture, so every test starts from a clean observation state.
func newProbeContext(t *testing.T) (context.Context, *logCapture) {
	t.Helper()
	resolverProbe.reset()
	capture := &logCapture{}
	return capture.intoContext(context.Background()), capture
}

// testNamespace is the namespace every fixture in this file lives in.
const testNamespace = "default"

func valkeyPodObj(name string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func withCertManagerTLS(v *vkov1.Valkey) {
	v.Spec.TLS = &vkov1.TLSSpec{
		Enabled: true,
		CertManager: &vkov1.CertManagerSpec{
			Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "test-issuer"},
		},
	}
}

// valkeyCASecret is the Secret builder.ValkeyTLSSecretName resolves to for the
// "test" cluster used throughout this file.
func valkeyCASecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tls", Namespace: testNamespace},
		Data:       map[string][]byte{"ca.crt": []byte(testCACert)},
	}
}

func newFakeChecker(objs ...client.Object) *Checker {
	return NewChecker(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(objs...).Build())
}

// --- harness self-check ---

// TestDNSProbe_RecordsTheQueriedHostname guards the decoder: if a future Go
// release changes how the resolver frames queries, every "which address was
// dialled" assertion in this file would silently degrade to an empty set.
func TestDNSProbe_RecordsTheQueriedHostname(t *testing.T) {
	resolverProbe.reset()

	conn, err := net.DialTimeout("tcp", "probe-target.example.invalid:1", 2*time.Second)
	if conn != nil {
		_ = conn.Close()
	}

	require.Error(t, err, "the probe resolver must never resolve anything")
	assert.Contains(t, resolverProbe.hosts(), "probe-target.example.invalid")
}

// --- CheckCluster ---

func TestCheckCluster_FailureModes(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*vkov1.Valkey)
		objects         []client.Object
		wantErr         string
		wantTotal       int32
		wantProbedHosts []string
	}{
		{
			name:            "unreadable TLS secret aborts before any pod is dialled",
			mutate:          withCertManagerTLS,
			wantErr:         "TLS config: reading TLS secret test-tls",
			wantTotal:       2,
			wantProbedHosts: []string{},
		},
		{
			name:            "no pods exist at all",
			wantErr:         "no master found among 3 pods",
			wantTotal:       2,
			wantProbedHosts: []string{},
		},
		{
			name: "pods exist but none has reached Running",
			objects: []client.Object{
				valkeyPodObj("test-0", corev1.PodPending),
				valkeyPodObj("test-1", corev1.PodFailed),
				valkeyPodObj("test-2", corev1.PodSucceeded),
			},
			wantErr:         "no master found among 3 pods",
			wantTotal:       2,
			wantProbedHosts: []string{},
		},
		{
			name: "running pods that do not answer leave the cluster masterless",
			objects: []client.Object{
				valkeyPodObj("test-0", corev1.PodRunning),
				valkeyPodObj("test-1", corev1.PodRunning),
				valkeyPodObj("test-2", corev1.PodRunning),
			},
			wantErr:   "no master found among 3 pods",
			wantTotal: 2,
			wantProbedHosts: []string{
				"test-0.test-headless.default.svc.cluster.local",
				"test-1.test-headless.default.svc.cluster.local",
				"test-2.test-headless.default.svc.cluster.local",
			},
		},
		{
			name: "standalone expects zero replicas behind the master",
			mutate: func(v *vkov1.Valkey) {
				v.Spec.Replicas = 1
				v.Spec.Sentinel = nil
			},
			wantErr:         "no master found among 1 pods",
			wantTotal:       0,
			wantProbedHosts: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newProbeContext(t)
			opts := []func(*vkov1.Valkey){}
			if tc.mutate != nil {
				opts = append(opts, tc.mutate)
			}
			v := newTestValkey("test", "default", opts...)

			state := newFakeChecker(tc.objects...).CheckCluster(ctx, v)

			require.NotNil(t, state)
			require.Error(t, state.Error)
			assert.Contains(t, state.Error.Error(), tc.wantErr)
			assert.Equal(t, tc.wantTotal, state.TotalReplicas, "TotalReplicas is spec.replicas minus the master")
			assert.Empty(t, state.MasterPod)
			assert.Empty(t, state.MasterAddress)
			assert.Zero(t, state.ReadyReplicas)
			assert.False(t, state.AllSynced)
			assert.False(t, state.SentinelMonitoring, "sentinel is never consulted while the master is unknown")
			assert.Equal(t, tc.wantProbedHosts, resolverProbe.hosts())
		})
	}
}

// --- findMaster ---

func TestFindMaster_SkipsPodsThatAreNotRunning(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	checker := newFakeChecker(
		valkeyPodObj("test-0", corev1.PodPending),
		valkeyPodObj("test-1", corev1.PodRunning),
		// test-2 is absent entirely, which is the Get-error branch.
	)

	pod, addr, err := checker.findMaster(ctx, v, "", nil)

	require.Error(t, err)
	assert.EqualError(t, err, "no master found among 3 pods")
	assert.Empty(t, pod)
	assert.Empty(t, addr)
	assert.Equal(t, []string{"test-1.test-headless.default.svc.cluster.local"}, resolverProbe.hosts(),
		"a pod that is not Running must not be dialled at all")
}

// TestFindMaster_ScansEveryOrdinalOfTheStatefulSet pins the coverage of the scan:
// no ordinal may be skipped. The visit *order* is deliberately not asserted —
// the probes run concurrently since ADR 0019, so the arrival order carries no
// meaning, and the answer must not depend on it either
// (TestFindMaster_TiedMastersResolveToTheLowestOrdinal).
func TestFindMaster_ScansEveryOrdinalOfTheStatefulSet(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 5 })

	var mu sync.Mutex
	var gets []string
	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey,
				obj client.Object, opts ...client.GetOption) error {
				mu.Lock()
				gets = append(gets, key.String())
				mu.Unlock()
				return cl.Get(ctx, key, obj, opts...)
			},
		}).Build()

	_, _, err := NewChecker(c).findMaster(ctx, v, "", nil)

	require.EqualError(t, err, "no master found among 5 pods")
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(gets)
	assert.Equal(t, []string{
		"default/test-0", "default/test-1", "default/test-2", "default/test-3", "default/test-4",
	}, gets, "every ordinal is visited exactly once")
}

func TestFindMaster_ZeroReplicasScansNothing(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 0 })

	_, _, err := newFakeChecker().findMaster(ctx, v, "", nil)

	require.EqualError(t, err, "no master found among 0 pods")
	assert.Empty(t, resolverProbe.hosts())
}

// TestFindMaster_ClusterNameEndingInSentinelStillUsesTheDataService is the
// regression guard for the defect that made a CR name decide which tier a pod
// belongs to.
//
// A Valkey CR named "valkey-sentinel" produces the data pod
// "valkey-sentinel-0". The old podAddress read the eight characters before the
// ordinal, found "sentinel" in the *cluster* name, and dialled the data pods
// through the Sentinel headless Service -- which selects no data pod, so the
// health check could never find the master of such a cluster. The e2e clusters
// "term-sentinel" and "term-no-sentinel" sat in exactly that window.
//
// The address must come from the component the caller means, never from the
// name it happens to have.
func TestFindMaster_ClusterNameEndingInSentinelStillUsesTheDataService(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("valkey-sentinel", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })

	_, _, err := newFakeChecker(
		valkeyPodObj("valkey-sentinel-0", corev1.PodRunning),
	).findMaster(ctx, v, "", nil)

	require.Error(t, err)
	assert.Equal(t,
		[]string{"valkey-sentinel-0.valkey-sentinel-headless.default.svc.cluster.local"},
		resolverProbe.hosts(),
		"a data pod is addressed through the data headless service, whatever the cluster is called")
}

// --- PingPod ---

func TestPingPod_TLSConfigError(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", withCertManagerTLS)

	err := newFakeChecker().PingPod(ctx, v, "test-0")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS config for ping: reading TLS secret test-tls")
	assert.Empty(t, resolverProbe.hosts(), "no pod is dialled when the TLS config cannot be built")
}

func TestPingPod_ReportsTheAddressItDialled(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	err := newFakeChecker().PingPod(ctx, v, "test-3")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping test-3.test-headless.default.svc.cluster.local:6379",
		"the failure names the pod and the plaintext port")
	assert.Equal(t, []string{"test-3.test-headless.default.svc.cluster.local"}, resolverProbe.hosts())
}

func TestPingPod_UsesTheTLSPortWhenTLSIsEnabled(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", withCertManagerTLS)

	err := newFakeChecker(valkeyCASecret()).PingPod(ctx, v, "test-0")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping test-0.test-headless.default.svc.cluster.local:16379",
		"TLS clusters are pinged on 16379")
	assert.NotContains(t, err.Error(), "TLS config for ping")
}

func TestPingPod_AuthenticatedClusterStillDialsThePod(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "auth", SecretPasswordKey: "password"}
	})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("s3cr3t")},
	}

	err := newFakeChecker(secret).PingPod(ctx, v, "test-0")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping test-0.test-headless.default.svc.cluster.local:6379")
	assert.NotContains(t, err.Error(), "s3cr3t", "the password must not leak into the error")
}

// --- GetReplicationInfo ---

func TestGetReplicationInfo_TLSConfigError(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", withCertManagerTLS)

	info, err := newFakeChecker().GetReplicationInfo(ctx, v, "test-0")

	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "TLS config for replication info: reading TLS secret test-tls")
	assert.Empty(t, resolverProbe.hosts())
}

func TestGetReplicationInfo_UnreachablePodReturnsNoInfo(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default")

	info, err := newFakeChecker().GetReplicationInfo(ctx, v, "test-1")

	require.Error(t, err)
	assert.Nil(t, info, "an unreachable pod must never yield a zero-valued ReplicationInfo")
	assert.Contains(t, err.Error(), "info replication test-1.test-headless.default.svc.cluster.local:6379")
	assert.Equal(t, []string{"test-1.test-headless.default.svc.cluster.local"}, resolverProbe.hosts())
}

func TestGetReplicationInfo_UsesTheTLSPortWhenTLSIsEnabled(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", withCertManagerTLS)

	info, err := newFakeChecker(valkeyCASecret()).GetReplicationInfo(ctx, v, "test-0")

	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "info replication test-0.test-headless.default.svc.cluster.local:16379")
}

// --- checkSentinel ---

func TestCheckSentinel_UnreadableSentinelTLSSecretReturnsFalse(t *testing.T) {
	ctx, capture := newProbeContext(t)
	v := newTestValkey("test", "default", withCertManagerTLS)

	// The Valkey TLS secret exists, the Sentinel one does not: in the default
	// (non-unified) cert-manager mode those are two distinct Secrets.
	agreeing := newFakeChecker(valkeyCASecret()).observeSentinels(ctx, v).monitoring()

	assert.False(t, agreeing)
	assert.Contains(t, capture.joined(), "Could not build TLS config for sentinel health check")
	assert.Empty(t, resolverProbe.hosts(), "no sentinel is dialled without a TLS config")
}

func TestCheckSentinel_UnifiedCertificateSharesTheValkeySecret(t *testing.T) {
	ctx, capture := newProbeContext(t)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		withCertManagerTLS(v)
		v.Spec.TLS.UnifiedCertificate = true
	})

	// Only the Valkey secret exists. In unified mode that is the sentinel secret
	// too, so the check must proceed to the sentinels instead of bailing out.
	agreeing := newFakeChecker(valkeyCASecret()).observeSentinels(ctx, v).monitoring()

	assert.False(t, agreeing, "no sentinel answers, so there is no quorum")
	assert.NotContains(t, capture.joined(), "Could not build TLS config for sentinel health check")
	assert.Equal(t, []string{
		"test-sentinel-0.test-sentinel-headless.default.svc.cluster.local",
		"test-sentinel-1.test-sentinel-headless.default.svc.cluster.local",
		"test-sentinel-2.test-sentinel-headless.default.svc.cluster.local",
	}, resolverProbe.hosts())
}

func TestCheckSentinel_ProbedPodSet(t *testing.T) {
	tests := []struct {
		name      string
		sentinel  *vkov1.SentinelSpec
		wantHosts []string
	}{
		{
			name:     "replicas unset falls back to three sentinels",
			sentinel: &vkov1.SentinelSpec{Enabled: true},
			wantHosts: []string{
				"test-sentinel-0.test-sentinel-headless.default.svc.cluster.local",
				"test-sentinel-1.test-sentinel-headless.default.svc.cluster.local",
				"test-sentinel-2.test-sentinel-headless.default.svc.cluster.local",
			},
		},
		{
			name:     "configured replica count is honoured",
			sentinel: &vkov1.SentinelSpec{Enabled: true, Replicas: 5},
			wantHosts: []string{
				"test-sentinel-0.test-sentinel-headless.default.svc.cluster.local",
				"test-sentinel-1.test-sentinel-headless.default.svc.cluster.local",
				"test-sentinel-2.test-sentinel-headless.default.svc.cluster.local",
				"test-sentinel-3.test-sentinel-headless.default.svc.cluster.local",
				"test-sentinel-4.test-sentinel-headless.default.svc.cluster.local",
			},
		},
		{
			name:      "a single sentinel is still probed",
			sentinel:  &vkov1.SentinelSpec{Enabled: true, Replicas: 1},
			wantHosts: []string{"test-sentinel-0.test-sentinel-headless.default.svc.cluster.local"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newProbeContext(t)
			v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Sentinel = tc.sentinel })

			assert.False(t, newFakeChecker().observeSentinels(ctx, v).monitoring(),
				"a silent sentinel never counts towards the quorum")
			assert.Equal(t, tc.wantHosts, resolverProbe.hosts())
		})
	}
}

func TestCheckSentinel_PortDependsOnTLS(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*vkov1.Valkey)
		objects  []client.Object
		wantPort int
	}{
		{
			name:     "plaintext sentinels are probed on 26379",
			wantPort: builder.SentinelPort,
		},
		{
			name: "TLS sentinels are probed on 36379",
			mutate: func(v *vkov1.Valkey) {
				withCertManagerTLS(v)
				v.Spec.TLS.UnifiedCertificate = true
			},
			objects:  []client.Object{valkeyCASecret()},
			wantPort: builder.SentinelTLSPort,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, capture := newProbeContext(t)
			opts := []func(*vkov1.Valkey){}
			if tc.mutate != nil {
				opts = append(opts, tc.mutate)
			}
			v := newTestValkey("test", "default", opts...)

			assert.False(t, newFakeChecker(tc.objects...).observeSentinels(ctx, v).monitoring())
			assert.Contains(t, capture.joined(),
				fmt.Sprintf("test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:%d", tc.wantPort),
				"the V(1) diagnostic must name the address that was actually dialled")
		})
	}
}

// TestObserveSentinels_DoubleDigitOrdinalUsesTheSentinelService is the
// regression guard for the second face of the same defect: the marker was read
// at a fixed offset before the ordinal, so it was missed as soon as the ordinal
// had two digits and the Sentinel pod was dialled through the Valkey headless
// Service instead.
//
// Eleven sentinels is not a realistic configuration; it is the smallest input
// that reaches the defect, and spec.sentinel.replicas has no Maximum, so it is
// reachable rather than hypothetical.
func TestObserveSentinels_DoubleDigitOrdinalUsesTheSentinelService(t *testing.T) {
	ctx, _ := newProbeContext(t)
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 11}
	})

	assert.False(t, newFakeChecker().observeSentinels(ctx, v).monitoring())
	assert.Contains(t, resolverProbe.hosts(), "test-sentinel-10.test-sentinel-headless.default.svc.cluster.local",
		"a sentinel pod is addressed through the sentinel headless service at every ordinal")
	assert.NotContains(t, resolverProbe.hosts(), "test-sentinel-10.test-headless.default.svc.cluster.local")
}

// --- the tier of a pod is never read out of its name ---

// TestPodAddress_ComponentIsNeverDerivedFromTheName pins the rule that replaced
// the old name-sniffing podAddress: the caller says which tier it means, and the
// headless Service and the port are chosen together from that.
//
// The cases that end in "sentinel" and the two-digit ordinals are the ones the
// old fixed-offset guess got wrong, in both directions. They are kept as a
// table so a future shortcut that reads the tier out of the name again fails
// here first (docs/adr/0029-a-name-is-not-a-component.md, D1).
func TestPodAddress_ComponentIsNeverDerivedFromTheName(t *testing.T) {
	tests := []struct {
		name     string
		cluster  string
		podName  string
		sentinel bool
		tls      bool
		wantAddr string
	}{
		{
			name:     "data pod",
			cluster:  "test",
			podName:  "test-0",
			wantAddr: "test-0.test-headless.default.svc.cluster.local:6379",
		},
		{
			name:     "data pod of a cluster whose name ends in sentinel",
			cluster:  "valkey-sentinel",
			podName:  "valkey-sentinel-0",
			wantAddr: "valkey-sentinel-0.valkey-sentinel-headless.default.svc.cluster.local:6379",
		},
		{
			name:     "data pod of a cluster named exactly sentinel",
			cluster:  "sentinel",
			podName:  "sentinel-0",
			wantAddr: "sentinel-0.sentinel-headless.default.svc.cluster.local:6379",
		},
		{
			name:     "data pod with a two digit ordinal",
			cluster:  "test",
			podName:  "test-10",
			wantAddr: "test-10.test-headless.default.svc.cluster.local:6379",
		},
		{
			// The old guess sliced the name and needed a length guard to avoid
			// panicking here. Nothing reads the name any more, but the shortest
			// legal name stays covered.
			name:     "data pod of a single character cluster",
			cluster:  "t",
			podName:  "t-0",
			wantAddr: "t-0.t-headless.default.svc.cluster.local:6379",
		},
		{
			name:     "data pod on the TLS port",
			cluster:  "test",
			podName:  "test-0",
			tls:      true,
			wantAddr: "test-0.test-headless.default.svc.cluster.local:16379",
		},
		{
			name:     "sentinel pod with a single digit ordinal",
			cluster:  "test",
			podName:  "test-sentinel-0",
			sentinel: true,
			wantAddr: "test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379",
		},
		{
			name:     "sentinel pod with a two digit ordinal",
			cluster:  "test",
			podName:  "test-sentinel-10",
			sentinel: true,
			wantAddr: "test-sentinel-10.test-sentinel-headless.default.svc.cluster.local:26379",
		},
		{
			name:     "sentinel pod of a cluster whose name ends in sentinel",
			cluster:  "valkey-sentinel",
			podName:  "valkey-sentinel-sentinel-0",
			sentinel: true,
			wantAddr: "valkey-sentinel-sentinel-0.valkey-sentinel-sentinel-headless.default.svc.cluster.local:26379",
		},
		{
			name:     "sentinel pod on the TLS port",
			cluster:  "test",
			podName:  "test-sentinel-0",
			sentinel: true,
			tls:      true,
			wantAddr: "test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:36379",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := []func(*vkov1.Valkey){}
			if tc.tls {
				opts = append(opts, func(v *vkov1.Valkey) {
					v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
				})
			}
			v := newTestValkey(tc.cluster, "default", opts...)

			got := valkeyPodAddress(v, tc.podName)
			if tc.sentinel {
				got = sentinelPodAddress(v, tc.podName)
			}
			assert.Equal(t, tc.wantAddr, got)
		})
	}
}

// TestPodAddress_TheTwoTiersNeverShareAnAddress states the property the pairing
// exists for: whatever the cluster is called, a data pod and a Sentinel pod of
// the same cluster never produce the same FQDN, and neither borrows the other's
// Service or port.
//
// The cluster name is the adversarial one -- it ends in "sentinel", so under the
// old guess the data pod took the Sentinel Service and collided with the tier it
// is not part of.
func TestPodAddress_TheTwoTiersNeverShareAnAddress(t *testing.T) {
	v := newTestValkey("valkey-sentinel", "default")

	data := valkeyPodAddress(v, "valkey-sentinel-0")
	sentinel := sentinelPodAddress(v, "valkey-sentinel-sentinel-0")

	assert.NotEqual(t, data, sentinel)
	assert.Contains(t, data, ".valkey-sentinel-headless.")
	assert.Contains(t, sentinel, ".valkey-sentinel-sentinel-headless.")
	// Substring checks are treacherous here: "valkey-sentinel-headless" contains
	// "-sentinel-headless". Compare the Service segment itself.
	assert.Equal(t, "valkey-sentinel-headless", strings.Split(data, ".")[1])
	assert.Equal(t, "valkey-sentinel-sentinel-headless", strings.Split(sentinel, ".")[1])
}
