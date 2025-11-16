package clients

import (
	"agentic-automation/internal/config"

	"k8s.io/client-go/kubernetes/fake"
)

// NewKubernetesClientWithClientset constructs a KubernetesClient using a provided fake clientset (test only).
func NewKubernetesClientWithClientset(logger *config.AppLogger) *KubernetesClient {
	if logger == nil {
		logger = config.NewNopLogger()
	}
	return &KubernetesClient{
		clientset: fake.NewSimpleClientset(),
		namespace: "default",
		logger:    logger,
	}
}
