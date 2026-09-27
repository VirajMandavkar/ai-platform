#!/bin/bash
set -e

echo "=== Cleaning up old processes ==="

# Kill old Go proxy/sandbox processes
fuser -k 8080/tcp 2>/dev/null || true
fuser -k 8081/tcp 2>/dev/null || true

# Remove ALL ai-sandbox containers (base + candidate ones)
echo "Removing old Docker containers..."
docker ps -a --filter "name=ai-sandbox" --format '{{.Names}}' | xargs -r docker rm -f 2>/dev/null || true

echo "=== Building ==="
cd agent-sandbox
docker build -t ai-sandbox-image .

echo "=== Starting sandbox container ==="
docker run -d --name ai-sandbox-1 --add-host=host.docker.internal:host-gateway -v $(pwd)/workspace:/home/sandboxuser/workspace ai-sandbox-image
cd ..

echo "=== Starting proxy gateway ==="
cd proxy-gateway
export $(grep -v '^#' .env | xargs)
go run ./cmd/proxy/main.go > ../proxy.log 2>&1 &
PROXY_PID=$!
cd ..

echo "=== Starting agent sandbox server ==="
cd agent-sandbox
export $(grep -v '^#' .env | xargs)
go run ./cmd/sandbox/main.go > ../sandbox.log 2>&1 &
SANDBOX_PID=$!
cd ..

echo ""
echo "=== All services started ==="
echo "Proxy PID:   $PROXY_PID  (port 8080)"
echo "Sandbox PID: $SANDBOX_PID  (port 8081)"
echo "Docker containers:"
docker ps --filter "name=ai-sandbox" --format '  {{.Names}} ({{.Status}})'
echo ""
echo "Open http://localhost:8081 in your browser."
