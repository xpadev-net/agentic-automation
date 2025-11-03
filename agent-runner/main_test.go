package main

import (
	"os"
	"testing"
)

func TestConstructOperatorURL(t *testing.T) {
	tests := []struct {
		name           string
		namespace      string
		serviceName    string
		port           string
		expectedURL    string
		expectError    bool
		errorSubstring string
	}{
		{
			name:        "valid configuration",
			namespace:   "default",
			serviceName: "agent-operator",
			port:        "3000",
			expectedURL: "http://agent-operator.default.svc.cluster.local:3000",
			expectError: false,
		},
		{
			name:        "custom namespace",
			namespace:   "production",
			serviceName: "operator-service",
			port:        "8080",
			expectedURL: "http://operator-service.production.svc.cluster.local:8080",
			expectError: false,
		},
		{
			name:           "missing namespace",
			namespace:      "",
			serviceName:    "agent-operator",
			port:           "3000",
			expectError:    true,
			errorSubstring: "KUBERNETES_NAMESPACE",
		},
		{
			name:           "missing service name",
			namespace:      "default",
			serviceName:    "",
			port:           "3000",
			expectError:    true,
			errorSubstring: "OPERATOR_SERVICE_NAME",
		},
		{
			name:           "missing port",
			namespace:      "default",
			serviceName:    "agent-operator",
			port:           "",
			expectError:    true,
			errorSubstring: "OPERATOR_SERVICE_PORT",
		},
		{
			name:           "all missing",
			namespace:      "",
			serviceName:    "",
			port:           "",
			expectError:    true,
			errorSubstring: "KUBERNETES_NAMESPACE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			if tt.namespace != "" {
				os.Setenv("KUBERNETES_NAMESPACE", tt.namespace)
			} else {
				os.Unsetenv("KUBERNETES_NAMESPACE")
			}
			if tt.serviceName != "" {
				os.Setenv("OPERATOR_SERVICE_NAME", tt.serviceName)
			} else {
				os.Unsetenv("OPERATOR_SERVICE_NAME")
			}
			if tt.port != "" {
				os.Setenv("OPERATOR_SERVICE_PORT", tt.port)
			} else {
				os.Unsetenv("OPERATOR_SERVICE_PORT")
			}

			// Clean up after test
			defer func() {
				os.Unsetenv("KUBERNETES_NAMESPACE")
				os.Unsetenv("OPERATOR_SERVICE_NAME")
				os.Unsetenv("OPERATOR_SERVICE_PORT")
			}()

			// Call the function
			url, err := constructOperatorURL()

			// Check error expectations
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if tt.errorSubstring != "" && !contains(err.Error(), tt.errorSubstring) {
					t.Errorf("expected error to contain %q, but got: %v", tt.errorSubstring, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if url != tt.expectedURL {
					t.Errorf("expected URL %q, but got %q", tt.expectedURL, url)
				}
			}
		})
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
