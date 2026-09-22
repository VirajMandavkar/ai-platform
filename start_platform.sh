#!/bin/bash
set -e

echo "Building agent sandbox Docker image..."
cd agent-sandbox
docker build -t ai-sandbox-image .

echo "Starting sandbox container..."
docker rm -f ai-sandbox-1 || true
docker run -d --name ai-sandbox-1 --add-host=host.docker.internal:host-gateway -v $(pwd)/workspace:/home/sandboxuser/workspace ai-sandbox-image
cd ..

echo "Starting proxy gateway..."
cd proxy-gateway
export PORT=8080
go run ./cmd/proxy/main.go > ../proxy.log 2>&1 &
PROXY_PID=$!
cd ..

echo "Starting agent sandbox server..."
cd agent-sandbox
go run ./cmd/sandbox/main.go > ../sandbox.log 2>&1 &
SANDBOX_PID=$!
cd ..

echo "Services started."
echo "Proxy PID: $PROXY_PID"
echo "Sandbox PID: $SANDBOX_PID"
