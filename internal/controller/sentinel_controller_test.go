package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

func TestSentinelReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = vkov1.AddToScheme(scheme)

	sentinel := &vkov1.Sentinel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sentinel",
			Namespace: "default",
		},
		Spec: vkov1.SentinelSpec{
			Replicas: 3,
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sentinel).
		Build()

	reconciler := &SentinelReconciler{
		Client: client,
		Scheme: scheme,
	}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-sentinel",
			Namespace: "default",
		},
	})

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}
