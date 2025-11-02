module agent-runner

// agent-runner: Go binary for executing AI agents in Kubernetes Pods
// Dependencies:
// - cobra: CLI framework for command-line interface
// - HTTP client: Standard library net/http (no external dependency required)
//   Reporter client (T038) will use standard library for exponential backoff retry

go 1.24.0

toolchain go1.24.9

require (
	github.com/spf13/cobra v1.8.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
)
