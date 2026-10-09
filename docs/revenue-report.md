# Revenue report

The Revenue page is available from a project's sidebar at `/:org/:site/revenue` on identified-tracking instances. It uses the project's permanent currency and project timezone, and supports the shared date range, filters, imported-data choice, and previous-period or previous-year comparison.

The report makes four independent, cached query requests:

- a time series for `total_revenue` and `average_revenue`, grouped by `time` and limited to 400 buckets;
- source, channel, and campaign breakdowns for the same metrics, each ordered by total revenue and limited to 10 rows.

The time series and headline totals include all events in the selected range with the permanent project currency. Breakdowns use event-time attribution. Closed legacy events that predate stored event attribution remain in unfiltered headline totals but are excluded from attribution breakdowns. A source, channel, or campaign filter also makes the headline and trend attribution-specific. The API reports affected current or comparison results as `event_attribution.coverage: available_only`; the page labels them as available attributed revenue. It does not substitute session acquisition data or imply complete historical attribution.

Projects with no configured permanent currency see a setup state. Projects with a historical currency conflict see a conflict state. Neither state issues revenue queries. Anonymous-tracking instances are stopped by the analytics privacy boundary before the route mounts.

This report does not perform currency conversion, refund accounting, provider reconciliation, or historical attribution reconstruction.
