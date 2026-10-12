CREATE TABLE replay_saved (
    site_id integer NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, user_id, session_id)
);

CREATE INDEX replay_saved_user_idx ON replay_saved (user_id);

CREATE FUNCTION delete_replay_saved_marker() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM replay_saved
    WHERE site_id = OLD.site_id AND session_id = OLD.session_id;
    RETURN OLD;
END;
$$;

CREATE TRIGGER replay_sessions_delete_saved
AFTER DELETE ON replay_sessions
FOR EACH ROW EXECUTE FUNCTION delete_replay_saved_marker();
