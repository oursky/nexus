const express = require('express');
const { Pool } = require('pg');

const app = express();
const port = process.env.PORT || 3000;

const pool = new Pool({ connectionString: process.env.DATABASE_URL });

app.use(express.json());

// CORS for local SPA dev
app.use((req, res, next) => {
  res.setHeader('Access-Control-Allow-Origin', '*');
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, DELETE, OPTIONS');
  res.setHeader('Access-Control-Allow-Headers', 'Content-Type');
  if (req.method === 'OPTIONS') return res.sendStatus(204);
  next();
});

app.get('/health', async (req, res) => {
  try {
    await pool.query('SELECT 1');
    res.json({ status: 'ok', db: 'connected' });
  } catch (err) {
    res.status(503).json({ status: 'error', db: err.message });
  }
});

app.get('/api/items', async (req, res) => {
  const { rows } = await pool.query('SELECT * FROM items ORDER BY created_at DESC');
  res.json(rows);
});

app.post('/api/items', async (req, res) => {
  const { name } = req.body;
  if (!name) return res.status(400).json({ error: 'name is required' });
  const { rows } = await pool.query(
    'INSERT INTO items (name) VALUES ($1) RETURNING *',
    [name]
  );
  res.status(201).json(rows[0]);
});

app.delete('/api/items/:id', async (req, res) => {
  await pool.query('DELETE FROM items WHERE id = $1', [req.params.id]);
  res.sendStatus(204);
});

app.listen(port, '0.0.0.0', () => {
  console.log(`API listening on :${port}`);
});
