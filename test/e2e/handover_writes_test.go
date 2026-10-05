//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// The writer harness of ADR 0037: a client writes through the -rw Service while the
// Sentinel data tier rolls, and the test counts what the roll did to the writes the
// client was told had succeeded.
//
// A write is acknowledged only when its reply is exactly "OK". valkey-cli exits 0 on
// an error reply too (READONLY, NOREPLICAS, LOADING), so the exit code proves
// nothing; the reply is classified instead (ADR 0038 D4). A write that got no
// reply at all -- a connection refused, reset or paused until disconnected -- is
// counted as not acknowledged: the client was never told it succeeded.
//
// The writer runs inside the cluster because the pod network is unreachable from
// the host on Kind under Docker Desktop, and it opens one connection per write
// through the Service, so every write follows the -rw endpoint as it moves.

const (
	// handoverWriterPod is the name of the in-cluster writer.
	handoverWriterPod = "handover-writer"
	// handoverWriterKeyPrefix prefixes every key the writer sets; the value is the
	// sequence number.
	handoverWriterKeyPrefix = "hw:"
)

// handoverWriterScript writes SET hw:<n> <n> through the -rw Service, one
// valkey-cli process and connection per write, and prints one line per write:
// "W <n> <reply>". It stops when /work/stop exists and prints "DONE <n>".
//
// Every write is bounded: a connect to an endpoint that vanished under the Service
// can hang without an answer (measured once, right after the outgoing master's
// delete: one write blocked the writer for the rest of the roll), so valkey-cli
// gets a connect timeout and the whole call a hard one. A write cut off that way has
// no reply and counts as not acknowledged, which is what the client saw.
const handoverWriterScript = `i=0
while [ ! -f /work/stop ]; do
  i=$((i+1))
  r=$(timeout 10 valkey-cli -t 2 -h "$RW_HOST" -p 6379 SET "` + handoverWriterKeyPrefix + `$i" "$i" 2>&1 | tr '\r\n' '  ')
  echo "W $i $r"
done
echo "DONE $i"
`

// writerLine matches one write of the writer's log.
var writerLine = regexp.MustCompile(`^W (\d+) ?(.*)$`)

// handoverWrites is the parsed writer log.
type handoverWrites struct {
	// acked holds every sequence number whose reply was exactly OK.
	acked map[int]bool
	// refused counts the writes whose reply was anything else, by reply text with
	// the per-attempt detail trimmed.
	refused map[string]int
	// last is the highest sequence number the log contains.
	last int
}

// parseHandoverWrites reads a writer log.
func parseHandoverWrites(log string) handoverWrites {
	w := handoverWrites{acked: map[int]bool{}, refused: map[string]int{}}
	scanner := bufio.NewScanner(strings.NewReader(log))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		m := writerLine.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if m == nil {
			continue
		}
		seq, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if seq > w.last {
			w.last = seq
		}
		reply := strings.TrimSpace(m[2])
		if reply == "OK" {
			w.acked[seq] = true
			continue
		}
		w.refused[refusalClass(reply)]++
	}
	return w
}

// refusalClass shortens a reply to the part that names its kind, so a summary
// groups "Could not connect to Valkey at x:6379: Connection refused" with its
// siblings instead of listing every one.
func refusalClass(reply string) string {
	if reply == "" {
		return "<no reply>"
	}
	if strings.HasPrefix(reply, "Could not connect") {
		if i := strings.LastIndex(reply, ": "); i >= 0 {
			return "Could not connect: " + reply[i+2:]
		}
	}
	fields := strings.Fields(reply)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	return strings.Join(fields, " ")
}

// ackedIn counts the acknowledged writes with a sequence number in (from, to].
func (w handoverWrites) ackedIn(from, to int) int {
	n := 0
	for seq := range w.acked {
		if seq > from && seq <= to {
			n++
		}
	}
	return n
}

// TestE2E_RollingUpdate_HA_WritesDuringHandover rolls the data tier of a Sentinel
// cluster while a client writes through -rw, and counts the acknowledged writes the
// final master does not hold (ADR 0037 D1, Consequences).
//
// On a Sentinel that runs a coordinated failover (Valkey 9.0 and later) none may be
// lost. On a Valkey 8 Sentinel the roll falls back to the forced failover, which
// loses the writes the outgoing master acknowledges until Sentinel converts it; the
// count is logged, and the fallback must be visible in the operator log.
//
// Not parallel: the measurement is a write rate against a failover window, and the
// other rolling-update tests stay sequential for the same reason.
func TestE2E_RollingUpdate_HA_WritesDuringHandover(t *testing.T) {
	tc := newTestClients(t)
	ns := "e2e-handover-writes"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "hw"
	image := testimages.Default()
	coordinated := sentinelRunsCoordinatedFailover(image)

	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    image,
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")
	outgoing := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, outgoing, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)
	outgoingPod := tc.getPod(t, ns, outgoing)
	t.Logf("Image %s, coordinated failover expected: %t, outgoing master %s", image, coordinated, outgoing)

	tc.startHandoverWriter(t, ns, fmt.Sprintf("%s-rw", name), image)
	defer func() {
		_ = tc.kube.CoreV1().Pods(ns).Delete(context.Background(), handoverWriterPod, metav1.DeleteOptions{})
	}()
	tc.waitForWriterAcks(t, ns, 200)

	// The outgoing master's sidecar log dies with the pod, and it is the only place
	// a second failover sent by its drain handler is logged.
	sidecarLog := tc.followContainerLog(ns, outgoing, "sidecar")

	rollStart := time.Now()
	seqAtTrigger := tc.writerLastSeq(t, ns)
	tc.patchValkeySpec(t, ns, name, map[string]interface{}{"resources": map[string]interface{}{
		"requests": map[string]interface{}{"cpu": "20m"},
	}})
	t.Logf("Roll triggered at writer seq %d", seqAtTrigger)

	seqAtDelete := tc.waitForOutgoingMasterDelete(t, ns, name, outgoingPod)

	tc.waitForAllPodsCPURequest(t, ns, name, 3, "20m")
	tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	seqAtDone := tc.writerLastSeq(t, ns)

	// A few more seconds of writes after the roll, so a loss at its very end is inside
	// the measured window too.
	time.Sleep(5 * time.Second)
	writes := tc.stopHandoverWriter(t, ns)

	finalMaster := tc.findMasterPod(t, ns, name, 3)
	present := tc.scanWriterKeys(t, ns, finalMaster)

	var lost []int
	for seq := range writes.acked {
		if !present[seq] {
			lost = append(lost, seq)
		}
	}
	lostBeforeTrigger := 0
	for _, seq := range lost {
		if seq <= seqAtTrigger {
			lostBeforeTrigger++
		}
	}

	switchMasters := tc.countSentinelSwitchMasters(ns, name, 3, rollStart)
	drainFailover := strings.Contains(sidecarLog(), "sentinel failover triggered")
	operatorLines := tc.operatorLogLines(ns, name, rollStart, "ailover")

	t.Logf("MEASUREMENT image=%s writes=%d acked=%d ackedLost=%d (before trigger %d) refused=%d "+
		"ackedTriggerToDelete=%d ackedDeleteToDone=%d switchMaster=%d drainFailoverOnOutgoing=%t final master=%s",
		image, writes.last, len(writes.acked), len(lost), lostBeforeTrigger, sumCounts(writes.refused),
		writes.ackedIn(seqAtTrigger, seqAtDelete), writes.ackedIn(seqAtDelete, seqAtDone),
		switchMasters, drainFailover, finalMaster)
	for reply, n := range writes.refused {
		t.Logf("  refused %6d x %s", n, reply)
	}
	t.Logf("Operator failover log lines since the trigger:\n%s", indentLines(strings.Join(operatorLines, "\n"), "  "))
	t.Logf("Drain lines of the outgoing master's sidecar:\n%s", indentLines(keepLinesContaining(sidecarLog(), "drain"), "  "))

	// The measurement proves something only if writes were acknowledged on both sides
	// of the handover: before the outgoing master was deleted and after.
	require.Positive(t, writes.ackedIn(seqAtTrigger, seqAtDelete),
		"no write was acknowledged between the trigger and the outgoing master's delete; the window was not measured")
	require.Positive(t, writes.ackedIn(seqAtDelete, seqAtDone),
		"no write was acknowledged between the outgoing master's delete and the end of the roll; the window was not measured")

	assert.Zero(t, lostBeforeTrigger, "the pre-roll dataset must survive the roll")
	tc.requireNoWarningEventsSince(t, ns, name, rollStart)

	if coordinated {
		assert.Empty(t, lost, "a coordinated failover must lose no acknowledged write")
		assert.Equal(t, 1, switchMasters, "the roll fails over exactly once")
		assert.False(t, drainFailover, "the outgoing master's drain handler must not fail over a second time")
		assert.True(t, containsAll(operatorLines, "failover triggered successfully", "\"coordinated\""),
			"the trigger must log failoverMode coordinated")
		return
	}
	// Two lines, not one: the fallback is logged where the Sentinel refused the option, and
	// the failover that goes through may be a later one -- a forced command right after the
	// fallback can meet NOGOODSLAVE on every Sentinel and succeed only on the retrigger
	// (measured once on Kind, 8.1.9), which logs no fallback of its own.
	assert.True(t, containsAll(operatorLines, "fallbackReason", "wrong number of arguments"),
		"a Valkey 8 Sentinel refuses COORDINATED and the trigger must log the fallback with the reply")
	assert.True(t, containsAll(operatorLines, "failover triggered successfully", "failoverMode", "forced"),
		"the failover that goes through on a Valkey 8 Sentinel is the forced one")
	assert.False(t, containsAll(operatorLines, "failover triggered successfully", "\"coordinated\""),
		"no Valkey 8 Sentinel runs a coordinated failover")
}

// sentinelRunsCoordinatedFailover reports whether the Sentinel of this image knows
// SENTINEL FAILOVER <name> COORDINATED, which Valkey added in 9.0.
func sentinelRunsCoordinatedFailover(image string) bool {
	tag := image
	if i := strings.LastIndex(tag, ":"); i >= 0 {
		tag = tag[i+1:]
	}
	major, err := strconv.Atoi(strings.SplitN(tag, ".", 2)[0])
	return err == nil && major >= 9
}

// startHandoverWriter creates the writer pod on the Valkey image, which carries
// valkey-cli, and waits for it to run.
func (tc *testClients) startHandoverWriter(t *testing.T, namespace, rwHost, image string) {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: handoverWriterPod, Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:         "writer",
				Image:        image,
				Command:      []string{"sh", "-c", handoverWriterScript},
				Env:          []corev1.EnvVar{{Name: "RW_HOST", Value: rwHost}},
				VolumeMounts: []corev1.VolumeMount{{Name: "work", MountPath: "/work"}},
			}},
			Volumes: []corev1.Volume{{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		},
	}
	_, err := tc.kube.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	require.NoError(t, err, "creating the writer pod")
	tc.waitForPodReady(t, namespace, handoverWriterPod)
}

// writerLog reads the writer's log, or its last tailLines lines when tailLines > 0.
func (tc *testClients) writerLog(t *testing.T, namespace string, tailLines int64) string {
	t.Helper()
	opts := &corev1.PodLogOptions{Container: "writer"}
	if tailLines > 0 {
		opts.TailLines = &tailLines
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	raw, err := tc.kube.CoreV1().Pods(namespace).GetLogs(handoverWriterPod, opts).DoRaw(ctx)
	require.NoError(t, err, "reading the writer log")
	return string(raw)
}

// writerLastSeq is the sequence number of the writer's most recent write.
func (tc *testClients) writerLastSeq(t *testing.T, namespace string) int {
	t.Helper()
	return parseHandoverWrites(tc.writerLog(t, namespace, 50)).last
}

// waitForWriterAcks waits until the writer has had n writes acknowledged, which
// proves the path through -rw works before anything is measured.
func (tc *testClients) waitForWriterAcks(t *testing.T, namespace string, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(parseHandoverWrites(tc.writerLog(t, namespace, 0)).acked) >= n
	}, 2*time.Minute, 2*time.Second, "the writer never had %d writes acknowledged through -rw", n)
}

// stopHandoverWriter asks the writer to stop, waits for its last line and returns
// the whole log parsed.
func (tc *testClients) stopHandoverWriter(t *testing.T, namespace string) handoverWrites {
	t.Helper()
	tc.kubectlExecBestEffort(namespace, handoverWriterPod, "writer", "touch", "/work/stop")
	var log string
	require.Eventually(t, func() bool {
		log = tc.writerLog(t, namespace, 0)
		return strings.Contains(log, "\nDONE ")
	}, time.Minute, time.Second, "the writer did not stop")
	return parseHandoverWrites(log)
}

// kubectlExecBestEffort runs a command in a container and ignores its outcome.
func (tc *testClients) kubectlExecBestEffort(namespace, pod, container string, command ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := append([]string{"exec", pod, "-n", namespace, "-c", container, "--"}, command...)
	_ = exec.CommandContext(ctx, "kubectl", args...).Run()
}

// scanWriterKeys returns the sequence numbers of every writer key the pod holds.
func (tc *testClients) scanWriterKeys(t *testing.T, namespace, pod string) map[int]bool {
	t.Helper()
	out := tc.valkeyExec(t, namespace, pod, 6379, "--scan", "--pattern", handoverWriterKeyPrefix+"*", "--count", "1000")
	present := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		seq, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(line), handoverWriterKeyPrefix))
		if err == nil {
			present[seq] = true
		}
	}
	return present
}

// waitForOutgoingMasterDelete polls the outgoing master until it carries a
// deletionTimestamp or has been replaced, logs every data pod's replication view at
// that moment and returns the writer's sequence number then.
func (tc *testClients) waitForOutgoingMasterDelete(t *testing.T, namespace, name string, outgoing *corev1.Pod) int {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), 250*time.Millisecond, rollingUpdateTimeout, true,
		func(ctx context.Context) (bool, error) {
			pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, outgoing.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			if err != nil {
				return false, nil
			}
			return pod.UID != outgoing.UID || pod.DeletionTimestamp != nil, nil
		})
	require.NoError(t, err, "the outgoing master %s was never deleted", outgoing.Name)
	seq := tc.writerLastSeq(t, namespace)
	t.Logf("Outgoing master %s deleted at writer seq %d; replication view of every data pod:", outgoing.Name, seq)
	for i := 0; i < 3; i++ {
		pod := fmt.Sprintf("%s-%d", name, i)
		info := tc.valkeyExecQuick(t, namespace, pod, 6379, "INFO", "replication")
		dbsize := tc.valkeyExecQuick(t, namespace, pod, 6379, "DBSIZE")
		t.Logf("  %s dbsize=%s\n%s", pod, dbsize, indentLines(keepReplicationFields(info), "    "))
	}
	return seq
}

// keepReplicationFields drops the offset and backlog lines of INFO replication.
func keepReplicationFields(info string) string {
	var kept []string
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"role:", "connected_slaves:", "slave", "master_host:",
			"master_link_status:", "master_sync_in_progress:", "master_failover_state:"} {
			if strings.HasPrefix(line, prefix) {
				kept = append(kept, line)
				break
			}
		}
	}
	return strings.Join(kept, "\n")
}

// waitForAllPodsCPURequest waits until every data pod carries the CPU request on its
// Valkey container and is Ready -- the roll the resources patch started is over.
func (tc *testClients) waitForAllPodsCPURequest(t *testing.T, namespace, name string, replicas int, cpu string) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), rollingUpdatePollInterval, rollingUpdateTimeout, true,
		func(ctx context.Context) (bool, error) {
			for i := 0; i < replicas; i++ {
				pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, fmt.Sprintf("%s-%d", name, i), metav1.GetOptions{})
				if err != nil || pod.DeletionTimestamp != nil || len(pod.Spec.Containers) == 0 {
					return false, nil
				}
				req := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
				if req.String() != cpu || !podIsReady(pod) {
					return false, nil
				}
			}
			return true, nil
		})
	require.NoError(t, err, "not every data pod of %s/%s reached cpu request %s", namespace, name, cpu)
}

// followContainerLog streams a container's log from now on until the container
// ends, and returns a function that reads what has arrived so far.
func (tc *testClients) followContainerLog(namespace, pod, container string) func() string {
	var mu sync.Mutex
	var buf strings.Builder
	since := metav1.Now()
	go func() {
		stream, err := tc.kube.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
			Container: container, Follow: true, SinceTime: &since,
		}).Stream(context.Background())
		if err != nil {
			mu.Lock()
			fmt.Fprintf(&buf, "<log stream of %s/%s failed: %v>\n", pod, container, err)
			mu.Unlock()
			return
		}
		defer func() { _ = stream.Close() }()
		chunk := make([]byte, 4096)
		for {
			n, readErr := stream.Read(chunk)
			mu.Lock()
			buf.Write(chunk[:n])
			mu.Unlock()
			if readErr == io.EOF || readErr != nil {
				return
			}
		}
	}()
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// countSentinelSwitchMasters returns the largest number of +switch-master events
// any Sentinel logged since the given moment: every Sentinel logs each failover it
// takes part in once.
func (tc *testClients) countSentinelSwitchMasters(namespace, name string, sentinels int, since time.Time) int {
	most := 0
	sinceTime := metav1.NewTime(since)
	for i := 0; i < sentinels; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		raw, err := tc.kube.CoreV1().Pods(namespace).GetLogs(fmt.Sprintf("%s-sentinel-%d", name, i),
			&corev1.PodLogOptions{Container: "sentinel", SinceTime: &sinceTime}).DoRaw(ctx)
		cancel()
		if err != nil {
			continue
		}
		if n := strings.Count(string(raw), "+switch-master"); n > most {
			most = n
		}
	}
	return most
}

// operatorLogLines returns the operator log lines since the given moment that name
// the namespace and contain substr.
func (tc *testClients) operatorLogLines(namespace, name string, since time.Time, substr string) []string {
	pods, err := tc.kube.CoreV1().Pods("valkey-operator-system").List(context.Background(),
		metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=valkey-operator"})
	if err != nil {
		return []string{fmt.Sprintf("<listing operator pods failed: %v>", err)}
	}
	sinceTime := metav1.NewTime(since)
	var lines []string
	for i := range pods.Items {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		raw, err := tc.kube.CoreV1().Pods("valkey-operator-system").GetLogs(pods.Items[i].Name,
			&corev1.PodLogOptions{SinceTime: &sinceTime}).DoRaw(ctx)
		cancel()
		if err != nil {
			lines = append(lines, fmt.Sprintf("<operator log of %s failed: %v>", pods.Items[i].Name, err))
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, namespace) && strings.Contains(line, name) && strings.Contains(line, substr) {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// containsAll reports whether one line holds every substring.
func containsAll(lines []string, substrs ...string) bool {
	for _, line := range lines {
		all := true
		for _, s := range substrs {
			if !strings.Contains(line, s) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// sumCounts adds up a count map.
func sumCounts(m map[string]int) int {
	total := 0
	for _, n := range m {
		total += n
	}
	return total
}
