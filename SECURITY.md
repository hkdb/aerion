# Security Policy

## Reporting a Vulnerability

We take the security of Aerion seriously. If you believe you have found a security vulnerability, please report it to us as described below.

### How to Report

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, please report them via email to: aerion@3df.io

You should receive a response within 72 hours. If for some reason you do not, please follow up via email to ensure we received your original message.

Please include the following information in your report:

- Type of issue (e.g., buffer overflow, SQL injection, cross-site scripting, etc.)
- Full paths of source file(s) related to the issue
- Location of the affected source code (tag/branch/commit or direct URL)
- Any special configuration required to reproduce the issue
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact of the issue, including how an attacker might exploit it

### What to Expect

- **Acknowledgment**: We will acknowledge receipt of your vulnerability report within 72 hours.
- **Communication**: We will keep you informed of the progress toward a fix and full announcement.
- **Credit**: We will credit you in our release notes and security advisories (unless you prefer to remain anonymous).

### Disclosure Policy

- We will work with you to understand and resolve the issue quickly.
- We request that you give us a reasonable amount of time to address the issue before public disclosure.
- We will coordinate the public disclosure with you.

## Security Best Practices for Users

### OAuth Credentials

If you are compiling Aerion from source:

1. **Never commit OAuth credentials** to version control
2. Use the `.env.example` file as a template and create your own `.env` file
3. Ensure `.env` is listed in `.gitignore` (it is by default)
4. Rotate your OAuth credentials periodically
5. Use separate OAuth applications for development and production

### Email Security

- Aerion stores emails locally on your device
- Use strong passwords for your email accounts
- Enable 2FA/MFA on your email accounts where possible
- For Gmail/Google Workspace: Use App-Specific Passwords or OAuth

### Data Storage

- Email data is stored in SQLite databases in your local data directory
- Ensure your device has appropriate security measures (disk encryption, screen lock, etc.)
- Back up your data regularly

## Security Features

Aerion includes the following security measures:

- **HTML Sanitization**: All HTML email content is sanitized before display to prevent XSS attacks
- **OAuth 2.0**: Secure authentication for Gmail and other OAuth-supporting providers
- **Local Storage**: Emails are stored locally, not on third-party servers
- **TLS/SSL**: All IMAP/SMTP connections use TLS encryption

## Known Limitations

- Aerion is currently in early stage active development
- Use at your own risk for sensitive communications
