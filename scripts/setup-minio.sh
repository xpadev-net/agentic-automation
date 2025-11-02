#!/bin/bash
#
# setup-minio.sh - MinIO/S3 bucket initialization script for agent-sessions
#
# This script creates the 'agent-sessions' bucket in MinIO or AWS S3 for
# storing AI agent session persistence data.
#
# Usage:
#   export S3_ENDPOINT=http://minio:9000
#   export S3_REGION=us-east-1
#   export S3_ACCESS_KEY_ID=minioadmin
#   export S3_SECRET_ACCESS_KEY=minioadmin
#   ./scripts/setup-minio.sh
#
# Required Environment Variables:
#   S3_ENDPOINT          - S3 API endpoint (e.g., http://minio:9000 or https://s3.amazonaws.com)
#   S3_REGION            - S3 region (e.g., us-east-1)
#   S3_ACCESS_KEY_ID     - S3 access key ID
#   S3_SECRET_ACCESS_KEY - S3 secret access key
#
# Optional Environment Variables:
#   S3_BUCKET            - Bucket name (default: agent-sessions)
#   S3_USE_PATH_STYLE    - Use path-style URLs (default: true, required for MinIO)
#
# Exit Codes:
#   0 - Success
#   1 - Error (missing requirements, client not found, bucket creation failed)
#

set -euo pipefail

# Global variables
MC_ALIAS=""
CLIENT_TYPE=""

# Display usage information
usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Create the agent-sessions bucket in MinIO or AWS S3 for session persistence.

Options:
    -h, --help    Show this help message and exit

Required Environment Variables:
    S3_ENDPOINT          S3 API endpoint URL
                        Example: http://minio:9000 (MinIO) or https://s3.amazonaws.com (AWS)
    
    S3_REGION            S3 region name
                        Example: us-east-1
    
    S3_ACCESS_KEY_ID     S3 access key ID for authentication
    
    S3_SECRET_ACCESS_KEY S3 secret access key for authentication

Optional Environment Variables:
    S3_BUCKET            Bucket name (default: agent-sessions)
    
    S3_USE_PATH_STYLE    Use path-style URLs (default: true, required for MinIO)
                        Set to 'false' for AWS S3 with virtual-hosted-style URLs

Client Priority:
    The script will automatically detect and use one of the following clients:
    1. MinIO Client (mc) - Preferred for MinIO servers
    2. AWS CLI (aws)     - Fallback, works with both MinIO and AWS S3

Examples:
    # Using MinIO with default credentials
    export S3_ENDPOINT=http://minio:9000
    export S3_REGION=us-east-1
    export S3_ACCESS_KEY_ID=minioadmin
    export S3_SECRET_ACCESS_KEY=minioadmin
    $0

    # Using AWS S3
    export S3_ENDPOINT=https://s3.amazonaws.com
    export S3_REGION=us-east-1
    export S3_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
    export S3_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
    export S3_USE_PATH_STYLE=false
    $0

    # Custom bucket name
    export S3_BUCKET=my-custom-bucket
    $0
EOF
}

# Check required environment variables and set defaults
check_requirements() {
    local missing=()

    # Check required variables
    if [[ -z "${S3_ENDPOINT:-}" ]]; then
        missing+=("S3_ENDPOINT")
    fi

    if [[ -z "${S3_REGION:-}" ]]; then
        missing+=("S3_REGION")
    fi

    if [[ -z "${S3_ACCESS_KEY_ID:-}" ]]; then
        missing+=("S3_ACCESS_KEY_ID")
    fi

    if [[ -z "${S3_SECRET_ACCESS_KEY:-}" ]]; then
        missing+=("S3_SECRET_ACCESS_KEY")
    fi

    # Show error if required variables are missing
    if [[ ${#missing[@]} -gt 0 ]]; then
        echo "Error: Missing required environment variables:" >&2
        printf "  - %s\n" "${missing[@]}" >&2
        echo "" >&2
        echo "Please set the required environment variables and try again." >&2
        echo "Run '$0 --help' for usage information." >&2
        exit 1
    fi

    # Set defaults for optional variables
    if [[ -z "${S3_BUCKET:-}" ]]; then
        export S3_BUCKET="agent-sessions"
    fi

    if [[ -z "${S3_USE_PATH_STYLE:-}" ]]; then
        export S3_USE_PATH_STYLE="true"
    fi

    # Normalize S3_USE_PATH_STYLE to lowercase for comparison
    S3_USE_PATH_STYLE=$(echo "${S3_USE_PATH_STYLE}" | tr '[:upper:]' '[:lower:]')
}

# Detect available S3 client (mc or aws)
detect_client() {
    if command -v mc >/dev/null 2>&1; then
        echo "MC"
    elif command -v aws >/dev/null 2>&1; then
        echo "AWS"
    else
        echo "Error: Neither MinIO client (mc) nor AWS CLI (aws) is installed." >&2
        echo "" >&2
        echo "Please install one of the following:" >&2
        echo "  - MinIO Client: https://min.io/docs/minio/linux/reference/minio-mc.html" >&2
        echo "  - AWS CLI: https://aws.amazon.com/cli/" >&2
        exit 1
    fi
}

# Create bucket using MinIO client (mc)
create_bucket_mc() {
    local endpoint="$1"
    local bucket="$2"
    local access_key="$3"
    local secret_key="$4"

    # Generate alias name
    MC_ALIAS="minio-setup-$$"

    echo "Using MinIO client (mc) to create bucket..."

    # Set up MinIO alias
    if ! mc alias set "${MC_ALIAS}" "${endpoint}" "${access_key}" "${secret_key}" >/dev/null 2>&1; then
        echo "Error: Failed to configure MinIO alias '${MC_ALIAS}'" >&2
        exit 1
    fi

    # Check if bucket already exists
    if mc ls "${MC_ALIAS}/${bucket}" >/dev/null 2>&1; then
        echo "Bucket '${bucket}' already exists. Skipping creation."
        return 0
    fi

    # Create bucket
    if ! mc mb "${MC_ALIAS}/${bucket}" >/dev/null 2>&1; then
        echo "Error: Failed to create bucket '${bucket}'" >&2
        exit 1
    fi

    echo "Bucket '${bucket}' created successfully using MinIO client."
}

# Create bucket using AWS CLI
create_bucket_aws() {
    local endpoint="$1"
    local region="$2"
    local bucket="$3"
    local access_key="$4"
    local secret_key="$5"
    local use_path_style="$6"

    echo "Using AWS CLI to create bucket..."

    # Export credentials for AWS CLI
    export AWS_ACCESS_KEY_ID="${access_key}"
    export AWS_SECRET_ACCESS_KEY="${secret_key}"
    export AWS_DEFAULT_REGION="${region}"

    # Determine if this is MinIO or AWS S3
    local is_minio=false
    if [[ "${use_path_style}" == "true" ]] || [[ "${endpoint}" =~ ^http:// ]]; then
        is_minio=true
    fi

    # Check if bucket already exists
    local check_cmd=("aws" "s3api" "head-bucket" "--bucket" "${bucket}")
    if [[ "${is_minio}" == "true" ]]; then
        check_cmd+=("--endpoint-url" "${endpoint}")
    fi

    if "${check_cmd[@]}" >/dev/null 2>&1; then
        echo "Bucket '${bucket}' already exists. Skipping creation."
        return 0
    fi

    # Create bucket
    local create_cmd=("aws" "s3api" "create-bucket" "--bucket" "${bucket}")

    if [[ "${is_minio}" == "true" ]]; then
        # MinIO: use endpoint-url
        create_cmd+=("--endpoint-url" "${endpoint}")
        create_cmd+=("--region" "${region}")
    else
        # AWS S3: region-specific handling
        if [[ "${region}" == "us-east-1" ]]; then
            # us-east-1 doesn't require LocationConstraint
            create_cmd+=("--region" "${region}")
        else
            # Other regions require LocationConstraint
            create_cmd+=("--region" "${region}")
            create_cmd+=("--create-bucket-configuration" "LocationConstraint=${region}")
        fi
    fi

    if ! "${create_cmd[@]}" >/dev/null 2>&1; then
        echo "Error: Failed to create bucket '${bucket}'" >&2
        exit 1
    fi

    # Verify bucket was created
    if ! "${check_cmd[@]}" >/dev/null 2>&1; then
        echo "Warning: Bucket creation reported success but verification failed." >&2
        echo "The bucket may not be accessible yet. Please check manually." >&2
        return 1
    fi

    echo "Bucket '${bucket}' created successfully using AWS CLI."
}

# Verify bucket existence
verify_bucket() {
    local client_type="$1"
    local bucket="$2"
    local alias_or_endpoint="$3"

    echo "Verifying bucket '${bucket}'..."

    if [[ "${client_type}" == "MC" ]]; then
        if mc ls "${alias_or_endpoint}/${bucket}" >/dev/null 2>&1; then
            echo "✓ Bucket '${bucket}' verified successfully."
        else
            echo "Warning: Could not verify bucket '${bucket}'." >&2
            echo "Bucket may have been created but is not accessible." >&2
            return 1
        fi
    elif [[ "${client_type}" == "AWS" ]]; then
        local verify_cmd=("aws" "s3api" "head-bucket" "--bucket" "${bucket}")
        
        # Check if we need endpoint-url (for MinIO)
        if [[ -n "${alias_or_endpoint:-}" ]] && [[ "${alias_or_endpoint}" =~ ^http:// ]]; then
            verify_cmd+=("--endpoint-url" "${alias_or_endpoint}")
        fi

        if "${verify_cmd[@]}" >/dev/null 2>&1; then
            echo "✓ Bucket '${bucket}' verified successfully."
        else
            echo "Warning: Could not verify bucket '${bucket}'." >&2
            echo "Bucket may have been created but is not accessible." >&2
            return 1
        fi
    fi
}

# Main execution function
main() {
    # Parse command line arguments
    if [[ $# -gt 0 ]]; then
        case "$1" in
            -h|--help)
                usage
                exit 0
                ;;
            *)
                echo "Error: Unknown option: $1" >&2
                echo "Run '$0 --help' for usage information." >&2
                exit 1
                ;;
        esac
    fi

    # Check requirements and set defaults
    check_requirements

    # Display configuration
    echo "Configuration:"
    echo "  Endpoint:     ${S3_ENDPOINT}"
    echo "  Region:       ${S3_REGION}"
    echo "  Bucket:       ${S3_BUCKET}"
    echo "  Path Style:   ${S3_USE_PATH_STYLE}"
    echo ""

    # Detect client
    CLIENT_TYPE=$(detect_client)
    echo "Detected client: ${CLIENT_TYPE}"
    echo ""

    # Create bucket based on client type
    local endpoint_for_verify="${S3_ENDPOINT}"
    
    if [[ "${CLIENT_TYPE}" == "MC" ]]; then
        create_bucket_mc \
            "${S3_ENDPOINT}" \
            "${S3_BUCKET}" \
            "${S3_ACCESS_KEY_ID}" \
            "${S3_SECRET_ACCESS_KEY}"
        endpoint_for_verify="${MC_ALIAS}"
    elif [[ "${CLIENT_TYPE}" == "AWS" ]]; then
        create_bucket_aws \
            "${S3_ENDPOINT}" \
            "${S3_REGION}" \
            "${S3_BUCKET}" \
            "${S3_ACCESS_KEY_ID}" \
            "${S3_SECRET_ACCESS_KEY}" \
            "${S3_USE_PATH_STYLE}"
        endpoint_for_verify="${S3_ENDPOINT}"
    fi

    # Verify bucket
    echo ""
    verify_bucket "${CLIENT_TYPE}" "${S3_BUCKET}" "${endpoint_for_verify}" || true

    echo ""
    echo "Setup completed successfully!"
}

# Execute main function
main "$@"

