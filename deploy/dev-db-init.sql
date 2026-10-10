-- Runs once, when the dev volume is first created (docker-compose.dev-db.yml).
-- Tests create their own schemas inside this database and drop them afterwards.
CREATE DATABASE supermetry_test OWNER supermetry;
