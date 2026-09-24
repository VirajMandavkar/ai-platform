#!/bin/bash
echo "Stopping platform services..."
fuser -k 8080/tcp 2>/dev/null || true
fuser -k 8081/tcp 2>/dev/null || true

echo "Stopping sandbox docker containers..."
docker rm -f $(docker ps -a -q --filter "name=ai-sandbox") 2>/dev/null || true

echo "All services and containers stopped cleanly."
