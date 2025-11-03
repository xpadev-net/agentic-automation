//go:build test

package clients

import (
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes/fake"
)

// NewKubernetesClientWithClientset constructs a KubernetesClient using a provided fake clientset (test only).
func NewKubernetesClientWithClientset(logger *zap.Logger) *KubernetesClient {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}
	return &KubernetesClient{
		clientset: fake.NewSimpleClientset(),
		namespace: "default",
		logger:    logger,
	}
}
