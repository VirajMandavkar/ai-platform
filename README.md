# VibeScout: AI-Native Assessment Platform

Welcome to **VibeScout**, the world's first engineering assessment platform built specifically for the age of AI. We don't test syntax memorization; we test architectural intuition, AI synergy, and prompt churn.

## 🌟 The VibeScout Advantage
Traditional interviews are obsolete. VibeScout evaluates candidates by dropping them into a fully-functional, locked-down workspace equipped *only* with an AI agent (Claude Code). 

We generate proprietary telemetry to measure:
- **Prompt Churn:** How effectively does the candidate steer the AI?
- **Architectural Direction:** Does the candidate understand the root cause, or are they blindly pasting errors?
- **AI Reliance Index:** Balancing manual intuition vs. AI delegation.

## 🏗️ Architecture

VibeScout consists of three decoupled pillars:

1. **The Frontend (UI/UX) - `agent-sandbox/web`**
   - **`index.html`**: A high-converting, tech-forward marketing landing page.
   - **`dashboard.html`**: The Recruiter Dashboard for viewing cohort analytics.
   - **`sandbox.html`**: The Candidate Environment featuring a live `xterm.js` web terminal connected directly to the backend via WebSockets.

2. **The Agent Sandbox (Go) - `agent-sandbox/`**
   - Serves the frontend UI and WebSocket connections on `8081`.
   - Manages complete lifecycle isolation via Docker.
   - **Security First:** Strict anti-RCE patches, dynamic host-mounting for unique sessions, and process-death enforcement (if the AI agent crashes, the container dies).

3. **The Proxy Gateway (Go) - `proxy-gateway/`**
   - The Enterprise API Gateway running on `8080`.
   - **Key Pooling:** Distributes massive "Start Gun" cohort traffic across an array of Anthropic keys to prevent rate limits.
   - **Pre-Flight Engine:** Calculates token capacity math before a cohort launches.
   - **Anti-SSRF:** Hardened whitelist preventing malicious LLM-driven internal network requests.

## 🚀 How to Run

1. **Start the platform:**
   ```bash
   ./start_platform.sh
   ```
   *(This builds the Docker image and boots the Proxy Gateway and Sandbox servers).*

2. **Access the Application:**
   - Open your browser to [http://localhost:8081](http://localhost:8081).
   - Click "Try Candidate Sandbox" to experience the live Web Terminal.

## 💰 Unit Economics & Tiers

- **Managed AI Tier ($40 / session):** We handle the API limits and infrastructure. 90% Margin.
- **Enterprise BYOK Tier ($25 / session):** Bring Your Own Key. Customers plug in their Anthropic API Key, and our Pre-Flight engine dynamically manages the load. 99% Margin.
