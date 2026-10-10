# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems.

Use GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**.

Include what you found, how to reproduce it, the version or commit, and the impact you expect. You can expect an acknowledgement within a few days. We will keep you updated on the fix and credit you in the release notes unless you prefer otherwise.

## Supported versions

Until `v0.1.0` is released, only the latest commit on `main` is supported. After that, the latest release gets security fixes.

## Scope

In scope: the Supermetry server, dashboard, tracker script and the official API client. Out of scope: vulnerabilities in your reverse proxy, PostgreSQL or host configuration, and reports that need access to a deployment's secret key or database.
