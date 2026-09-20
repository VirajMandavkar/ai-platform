# Use a stable, minimal base
FROM ubuntu:22.04

# Prevent interactive prompts during package installation
ENV DEBIAN_FRONTEND=noninteractive

# 1. Install bare minimum dependencies (curl is needed for the Node setup script)
RUN apt-get update && apt-get install -y \
    curl \
    git \
    ca-certificates \
    python3 \
    golang-go \
    && rm -rf /var/lib/apt/lists/*

# 2. Install Node.js v22 (Required for Claude Code)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# 3. Create an unprivileged user (uid 1000) so the agent isn't root
RUN useradd -m -u 1000 -s /bin/bash sandboxuser

# 4. Configure npm to install global packages in the user's home directory
ENV NPM_CONFIG_PREFIX=/home/sandboxuser/.npm-global
ENV PATH="/home/sandboxuser/.npm-global/bin:${PATH}"

# 5. Drop down to the unprivileged user BEFORE installing tools
USER sandboxuser
WORKDIR /home/sandboxuser/workspace

# 6. Install the Claude Code CLI agent via npm
RUN npm install -g @anthropic-ai/claude-code

# 7. Route Claude Code traffic through our Phase 1 Go Proxy
ENV ANTHROPIC_BASE_URL="http://host.docker.internal:8080"
ENV ANTHROPIC_API_KEY="dummy-key-because-proxy-handles-auth"

# Keep the container alive indefinitely
CMD ["tail", "-f", "/dev/null"]