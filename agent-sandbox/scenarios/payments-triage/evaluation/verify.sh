#!/bin/bash
set -e

echo "=========================================="
echo "      AUTOMATED VERIFICATION RUNNER       "
echo "=========================================="
echo "Scenario: Payment Processor Race Condition"
echo "Timestamp: $(date)"
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/../workspace" 2>/dev/null || cd "$SCRIPT_DIR"
export GOWORK=off

echo "1. Checking code compilation..."
go build -o /dev/null ./...
echo "✓ Compilation successful."
echo ""

echo "2. Running Race Detector and Unit Tests..."
if go test -v -race -timeout 15s ./... ; then
    echo ""
    echo "=========================================="
    echo "   ✓ ALL VERIFICATION CHECKS PASSED!     "
    echo "   - Race condition eliminated.          "
    echo "   - Concurrency synchronization valid.  "
    echo "=========================================="
    exit 0
else
    echo ""
    echo "=========================================="
    echo "   ✗ VERIFICATION FAILED                 "
    echo "   - Race condition or test error detected."
    echo "=========================================="
    exit 1
fi
