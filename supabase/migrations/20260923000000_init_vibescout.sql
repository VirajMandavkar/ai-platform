-- ====================================================================
-- VibeScout AI Assessment Platform - Supabase PostgreSQL Schema
-- ====================================================================
-- Run this in the Supabase SQL Editor (https://supabase.com/dashboard/project/_/sql)

-- 1. Recruiters & Admin Accounts
CREATE TABLE IF NOT EXISTS recruiters (
    id SERIAL PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- 2. Cohorts (Recruiter Assessment Campaigns & Concurrent Pools)
CREATE TABLE IF NOT EXISTS cohorts (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    scenario_id TEXT NOT NULL,
    api_key TEXT,
    duration_mins INTEGER DEFAULT 45,
    safe_capacity_seats INTEGER DEFAULT 1,
    authorized_emails TEXT DEFAULT '[]',
    registered_candidates TEXT DEFAULT '{}',
    recruiter_username TEXT DEFAULT 'admin',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    cohort_url TEXT
);

-- 3. Candidate Interview Sessions
CREATE TABLE IF NOT EXISTS interviews (
    id TEXT PRIMARY KEY,
    candidate_name TEXT NOT NULL,
    candidate_email TEXT NOT NULL,
    scenario_id TEXT NOT NULL,
    scenario_title TEXT,
    api_key TEXT,
    target_provider TEXT DEFAULT 'managed',
    duration_mins INTEGER DEFAULT 45,
    status TEXT DEFAULT 'INVITED', -- INVITED, IN_PROGRESS, COMPLETED, TERMINATED_VIOLATION
    cohort_id TEXT,
    recruiter_username TEXT DEFAULT 'admin',
    scheduled_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    invite_url TEXT
);

-- 4. Session Telemetry & Recruiter Scoring Summary
CREATE TABLE IF NOT EXISTS telemetry_sessions (
    session_id TEXT PRIMARY KEY,
    candidate_name TEXT,
    scenario_id TEXT,
    start_time TIMESTAMPTZ DEFAULT NOW(),
    end_time TIMESTAMPTZ,
    events TEXT DEFAULT '[]',
    total_prompts INTEGER DEFAULT 0,
    total_tokens INTEGER DEFAULT 0,
    verification_runs INTEGER DEFAULT 0,
    final_passed BOOLEAN DEFAULT FALSE,
    integrity_score INTEGER DEFAULT 100,
    ai_efficiency_index INTEGER DEFAULT 100,
    verdict TEXT DEFAULT 'PENDING'
);

-- 5. God-Mode Playback Frames (PTY Terminal, Diffs, Prompts, Proctoring)
CREATE TABLE IF NOT EXISTS telemetry_frames (
    id SERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    time_offset DOUBLE PRECISION NOT NULL,
    type TEXT NOT NULL, -- PTY, DIFF, PROMPT, LOCKOUT, VERIFY
    data TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes for high-concurrency lookup and timeline scrubbing
CREATE INDEX IF NOT EXISTS idx_interviews_status ON interviews(status);
CREATE INDEX IF NOT EXISTS idx_interviews_cohort ON interviews(cohort_id);
CREATE INDEX IF NOT EXISTS idx_telemetry_frames_session ON telemetry_frames(session_id, time_offset ASC);

-- Default Recruiter Seed (username: admin / password: admin)
INSERT INTO recruiters (username, password_hash)
VALUES ('admin', 'admin')
ON CONFLICT (username) DO NOTHING;

-- Enable Supabase Realtime (CDC) for live God-Mode recruiter dashboards
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'supabase_realtime' AND tablename = 'interviews'
    ) THEN
        ALTER PUBLICATION supabase_realtime ADD TABLE interviews;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_publication_tables 
        WHERE pubname = 'supabase_realtime' AND tablename = 'telemetry_sessions'
    ) THEN
        ALTER PUBLICATION supabase_realtime ADD TABLE telemetry_sessions;
    END IF;
END $$;
