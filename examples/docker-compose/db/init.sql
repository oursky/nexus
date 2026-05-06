CREATE TABLE IF NOT EXISTS items (
  id         SERIAL PRIMARY KEY,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO items (name) VALUES
  ('Hello from Nexus'),
  ('Edit me in the workspace'),
  ('Delete me to test');
