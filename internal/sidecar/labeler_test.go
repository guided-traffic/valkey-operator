package sidecar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- Mock implementations ---

type mockRoleDetector struct {
	role string
	err  error
}

func (m *mockRoleDetector) DetectRole() (string, error) {
	return m.role, m.err
}

type mockPodPatcher struct {
	patches []patchRecord
	err     error
}

type patchRecord struct {
	namespace  string
	name       string
	labelKey   string
	labelValue string
}

func (m *mockPodPatcher) PatchLabel(_ context.Context, namespace, name, labelKey, labelValue string) error {
	m.patches = append(m.patches, patchRecord{
		namespace:  namespace,
		name:       name,
		labelKey:   labelKey,
		labelValue: labelValue,
	})
	return m.err
}

// --- Tests ---

func TestLabeler_DetectsAndPatchesMasterRole(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// Should have patched the label once (role didn't change after that).
	require.Len(t, patcher.patches, 1)
	assert.Equal(t, "default", patcher.patches[0].namespace)
	assert.Equal(t, "pod-0", patcher.patches[0].name)
	assert.Equal(t, common.LabelInstanceRole, patcher.patches[0].labelKey)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)

	// Health should be ready.
	assert.True(t, health.IsReady())
}

func TestLabeler_DetectsAndPatchesReplicaRole(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleReplica}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-1", "test-ns", 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, "test-ns", patcher.patches[0].namespace)
	assert.Equal(t, "pod-1", patcher.patches[0].name)
	assert.Equal(t, common.RoleReplica, patcher.patches[0].labelValue)
}

func TestLabeler_SkipsPatchWhenRoleUnchanged(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// Multiple polls happened, but only one patch (initial detection).
	assert.Len(t, patcher.patches, 1)
}

func TestLabeler_PatchesAgainOnRoleChange(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// Start polling in background.
	done := make(chan struct{})
	go func() {
		labeler.Run(ctx, health)
		close(done)
	}()

	// Wait for initial detection.
	time.Sleep(100 * time.Millisecond)

	// Simulate role change.
	detector.role = common.RoleReplica

	// Wait for the change to be detected.
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// Should have two patches: master -> replica.
	require.GreaterOrEqual(t, len(patcher.patches), 2)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)
	assert.Equal(t, common.RoleReplica, patcher.patches[1].labelValue)
}

func TestLabeler_HandlesDetectionError(t *testing.T) {
	detector := &mockRoleDetector{err: errors.New("connection refused")}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// No patches should have been made.
	assert.Empty(t, patcher.patches)

	// Health should NOT be ready.
	assert.False(t, health.IsReady())
}

func TestLabeler_HandlesPatchError(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{err: errors.New("forbidden")}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// Should have attempted the patch but lastRole should not be updated.
	// The patcher records the attempt even though it returned an error.
	assert.NotEmpty(t, patcher.patches)

	// Health should be ready (role was detected even though patch failed).
	assert.True(t, health.IsReady())
}

func TestLabeler_NilHealth(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	// Should not panic even with nil health server.
	labeler.Run(ctx, nil)

	require.Len(t, patcher.patches, 1)
}
