-- Merging issues points a source issue at its target (app_issues.merged_into,
-- added with the workflow columns). Events keep their own fingerprint and
-- grouping version, so unmerging by hash needs no new column; this index
-- serves the merged-issues list, the unmerge lookup and the foreign key.
CREATE INDEX app_issues_merged_into_idx ON app_issues (merged_into) WHERE merged_into IS NOT NULL;
