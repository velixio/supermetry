-- Progress of the history import for repositories with too many stars for the
-- REST listing. backfill_cursor is GitHub's GraphQL cursor of the last page
-- imported (newest to oldest); backfill_complete is set once the whole history
-- is on record, either by the full REST diff or by the GraphQL import.
ALTER TABLE github_star_repos
    ADD COLUMN backfill_cursor text,
    ADD COLUMN backfill_complete boolean NOT NULL DEFAULT false;
