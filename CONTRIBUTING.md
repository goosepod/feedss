# Contributing

Contributions are welcome. feedss is a small, self-hosted RSS reader, so changes should keep the application reliable, approachable, and easy to operate.

## Good Contributions

- Bug fixes with a clear description of the problem and the fix.
- Small feature improvements that fit the existing reader experience.
- Feed parsing and compatibility improvements.
- UI and accessibility polish that avoids unnecessary complexity.
- Improvements to pagination, unread state, migrations, packaging, or tests.

## Before Opening a Pull Request

- Keep the change focused.
- Avoid committing databases, private feed data, local configuration, credentials, secrets, or generated build output.
- Add or update tests when behavior changes.
- Mention what you tested manually, especially for feed parsing, reading order, unread state, or UI changes.

Useful checks:

```shell
go test ./...
npm run test:ui
node --check static/app.js
```

## Project Expectations

- Preserve stable newest-to-oldest reading order when loading additional articles.
- Treat feed content as untrusted input and sanitize it before display.
- Keep database migrations backward compatible with existing installations.
- Keep dependencies modest and prefer the standard library or existing project tools when practical.
- Prefer clear, readable code over clever code.
- Match the existing style before introducing new patterns.
- Keep behavior usable on both desktop and mobile layouts.

## Security

Do not open public issues or pull requests containing credentials, session cookies, private feed URLs or tokens, database contents, or other secrets. If you find a security issue, follow the private reporting process in [SECURITY.md](SECURITY.md).

## License

By contributing to feedss, you agree that your contribution will be licensed under the [GNU General Public License v3.0](LICENSE).
