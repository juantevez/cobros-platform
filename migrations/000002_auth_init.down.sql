-- migrations/000002_auth_init.down.sql
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
DROP EXTENSION IF EXISTS citext;
