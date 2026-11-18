-- API key hygiene: where and with what a key was last used (country and a
-- coarse client family only; never an IP address or a raw User-Agent), and a
-- roll link from a successor to the key it replaced. Step-up authentication:
-- the moment a session last re-entered its password. A session counts as
-- freshly authenticated at created_at until it re-authenticates.
ALTER TABLE api_keys
    ADD COLUMN last_used_country text CHECK (last_used_country IS NULL OR char_length(last_used_country) = 2),
    ADD COLUMN last_used_client  text CHECK (last_used_client IS NULL OR last_used_client IN ('mcp', 'curl', 'sdk-js', 'sdk-python', 'browser', 'other')),
    ADD COLUMN rolled_from_id    bigint REFERENCES api_keys (id) ON DELETE SET NULL;

ALTER TABLE user_sessions
    ADD COLUMN reauth_at timestamptz;
