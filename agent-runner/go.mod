module agent-runner

// agent-runner: Go binary for executing AI agents in Kubernetes Pods
// Dependencies:
// - cobra: CLI framework for command-line interface
// - HTTP client: Standard library net/http (no external dependency required)
//   Reporter client (T038) will use standard library for exponential backoff retry

go 1.24.0

toolchain go1.24.9

require (
	// S3 Session Persistence dependencies (T052_S3)
	github.com/aws/aws-sdk-go-v2 v1.39.5
	github.com/aws/aws-sdk-go-v2/config v1.31.16
	github.com/aws/aws-sdk-go-v2/credentials v1.18.20
	github.com/aws/aws-sdk-go-v2/service/s3 v1.89.1
	github.com/bradleyfalzon/ghinstallation/v2 v2.11.0
	github.com/cenkalti/backoff/v4 v4.3.0
	github.com/spf13/cobra v1.8.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/aws/smithy-go v1.23.1
	github.com/google/go-github/v57 v57.0.0
	golang.org/x/oauth2 v0.32.0
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.2 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.39.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/google/go-github/v62 v62.0.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
)

replace github.com/golang-jwt/jwt/v4 => github.com/golang-jwt/jwt/v4 v4.5.2
