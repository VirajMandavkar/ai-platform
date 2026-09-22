# AWS Production Architecture & DevOps Blueprint

This document outlines the multi-tenant, cloud-native architecture for deploying the **AI-Native Technical Screening Platform** on Amazon Web Services (AWS).

```
                             [CANDIDATE BROWSER]
                                      │
                         HTTPS (Port 443) / WSS (WebSocket)
                                      │
                                      ▼
                        ┌───────────────────────────┐
                        │   AWS Application (ALB)   │
                        │   • SSL / TLS Termination │
                        │   • Sticky Sessions (PTY) │
                        │   • DDoS (AWS Shield)     │
                        └─────────────┬─────────────┘
                                      │
           ┌──────────────────────────┴──────────────────────────┐
           │ Private VPC Subnet                                  │
           ▼                                                     ▼
┌─────────────────────────┐                           ┌─────────────────────────┐
│ Control Plane / Web App │                           │      Proxy Gateway      │
│ (ECS Fargate Service)   │                           │  (Universal SSE Adapter)│
│ • Port 8081             │                           │ • Port 8080             │
│ • Serves 3-Pane UI      │                           │ • BYOK Token Throttler  │
│ • Session Orchestration │                           └───────────┬─────────────┘
└───────────┬─────────────┘                                       │
            │                                                     │
            │ Spawns Ephemeral Sandbox                            ▼
            ▼                                         ┌─────────────────────────┐
┌──────────────────────────────────────┐              │ Upstream Model Provider │
│ Candidate Sandbox Execution Layer    │              │ (DeepSeek / OpenAI /    │
│                                      │              │  Azure OpenAI / Bedrock)│
│ Option 1: ECS Fargate Task           │              └─────────────────────────┘
│ Option 2: Firecracker on EC2         │
│ • Strict Egress Security Group       │
│ • Denies internal VPC access         │
│ • Denies AWS Metadata (169.254.x.x)  │
│ • Mounts S3/EFS assessment repo      │
└──────────────────┬───────────────────┘
                   │
                   ▼
┌──────────────────────────────────────┐
│ S3 Bucket & PostgreSQL RDS           │
│ • Real-time Asciinema Terminal Casts │
│ • Candidate Scorecards & Telemetry   │
│ • Test Scenarios & Manifest Bundles  │
└──────────────────────────────────────┘
```

---

## 1. Sandbox Execution Options

### Option A: AWS ECS Fargate (Fastest to Market & Operational Simplicity)
* **How it works**: For every candidate starting an interview, the Control Plane calls `ecs.RunTask`.
* **Container Lifecycle**:
  * The task boots an isolated container with dedicated CPU (1 vCPU) and RAM (2 GB).
  * Attached to its own ENI (Elastic Network Interface).
  * Auto-terminates using `stopTimeout` or after the 45-minute assessment window expires.
* **Security Hardening**:
  * Set `readonlyRootFilesystem: true` with a 2GB writable `tmpfs` mounted at `/home/sandboxuser/workspace`.
  * Drops all Linux capabilities (`cap_drop: ["ALL"]`) except `CHOWN`, `SETUID`, `SETGID`.
* **Network Egress Security Group**:
  * Inbound: Port 22/PTY from Control Plane only.
  * Outbound Rule 1: Allow TCP Port 8080 to the Proxy Gateway private IP.
  * Outbound Rule 2: Allow TCP 443 to npm / go proxy package registries.
  * Outbound Rule 3: **DENY** all other traffic (explicitly blocking AWS Metadata `169.254.169.254` and VPC CIDR `10.0.0.0/16`).

### Option B: Firecracker MicroVMs on EC2 Metal (Enterprise Hardware-Level Isolation)
* If your clients require hardware-enforced isolation against kernel exploits:
  * Deploy on `c6i.metal` or `c7i.metal` EC2 instances.
  * Firecracker boots a lightweight Linux kernel in **< 50ms** with < 5MB memory overhead.
  * Even if a candidate executes a rootkit or privilege escalation inside bash, they cannot break out to the host.

---

## 2. Real-Time Telemetry & Replay Persistence

* **Session Recording**: The PTY bridge writes frames to an S3 multipart upload stream (`s3://screening-telemetry/<sessionId>/session.cast`).
* **Audit Timeline**: Proctoring events (tab switches, full-screen blurs, paste volume) are saved into PostgreSQL (`candidate_audit_events` table).
* **HR Scorecard**: The final test verification result (`verify.sh` exit code and race report) is stored in the candidate record for instant review.

---

## 3. Cost Breakdown (Per 1,000 Completed Interviews)

| Component | Architecture Role | Est. Cost / 1,000 Interviews |
| :--- | :--- | :--- |
| **ECS Fargate** | 1 vCPU / 2GB RAM for 45 mins | ~$35.00 |
| **ALB & Data Transfer** | WebSocket terminal I/O streaming | ~$12.00 |
| **S3 Storage** | Raw terminal frames & audit logs | ~$1.50 |
| **DeepSeek API (BYOK)** | Client-provided key (0 cost to platform) | $0.00 (Customer pays ~$0.20) |
| **Total Cloud Infra Cost** | | **~$0.048 per candidate session** |

This gives the platform an enterprise SaaS gross margin of **> 95%** when charging customers $20 – $50 per completed screening!
