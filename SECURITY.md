# Security Policy

feedss connects to third-party feed servers and may handle feed URLs, usernames, password hashes, session cookies, article content, favicon and image requests, and local SQLite data. Please do not report sensitive security issues in public issues or pull requests.

## Reporting a Vulnerability

Use the repository's **Security** tab and select **Report a vulnerability** to submit a private GitHub security advisory. If private vulnerability reporting is unavailable, contact the project maintainer privately through GitHub.

Please include:

- A short description of the issue and its potential impact.
- Steps to reproduce it, if safe to share.
- The affected feedss version and deployment environment.
- Any relevant logs with credentials, tokens, private URLs, cookies, and personal data removed.

Please allow the maintainer time to investigate and prepare a fix before publicly disclosing the issue.

## Sensitive Information

Do not include any of the following in public reports:

- Account credentials or password hashes.
- Session cookies or authentication tokens.
- Private feed URLs or feed access tokens.
- SQLite database contents or backups.
- Private article content or unsanitized logs containing personal data.

## Supported Versions

Security fixes are handled on the current release and development line. Older releases may not receive separate patches unless the issue is severe and practical to backport.
