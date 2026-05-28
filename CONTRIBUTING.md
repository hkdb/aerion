# Contributing to Aerion

Thank you for your interest in contributing to Aerion! This document provides guidelines and instructions for contributing.

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for everyone.

## How to Contribute

### Reporting Bugs

1. Check if the bug has already been reported in [Issues](https://github.com/hkdb/aerion/issues)
2. If not, create a new issue with:
    - Select the `Bug` template and fill the form out in detail.

### Suggesting Features

1. Check existing issues for similar suggestions
2. Determine if your request is a brand new feature or an enahncement to an existing feature
3. Create a new issue with the `feature request` or `enhancement` template depending on step 2.

### Translation Pull Requests

`v0.1.39` is a milestone release. As basic features are complete and all seem to be relatively stable, starting with the `v0.2.0-dev` branch, Aerion is ready for translation contributions.

Please read the [Translation Contribution Guide](docs/LANGUAGE.md) thoroughly prior to submitting any pull requests.

### General Pull Requests

Aerion is currently in the rapid development state and aside from translation contributions, the workflow is not yet setup to accept pull requests. However, in the near future, we will transition to a workflow that will make accepting general PRs possible.

Meanwhile, below are some guidelines for contributions when we become ready to accept general PRs.

## Areas for Contribution

- **Bug fixes** - Check issues labeled `bug`
- **Documentation** - README, code comments, guides
- **Tests** - We need more test coverage
- **Accessibility** - Improving keyboard navigation and screen reader support
- **Performance** - Optimization opportunities
- **Features** - Check issues labeled `enhancement`

In order to ensure that we don't waste your efforts, prior to working on a PR, first submit an issue with the `Contribution` template and describe what you want to work on and any relevant details that will help the maintainer understand what you will achieve. A maintainer will review your issue and work with you to ensure there aren't any issues or overlapping efforts with your proposal. This will greatly increase the chances of you submitting a PR that yields positive results.

## Principles

Regardless of bug fix or feature implementation, all contributions must follow these principles:

- Security: always take a security-first approach
- Privacy: user privacy is a top priority
- Minimalist: always take the simplest approach and put forth best effort to have a clutter-free UI
- Lightweight: maintain one of the core values of Aerion -- keep the app lightweight in all aspects
- Efficiency: battery life on laptops is also a top priority -- minimize unnecessary resource consumption
- Flexible: accommodate a reasonable range of mainstream user preferences through configurable options
- Keyboard: ensure new UI components, features, and flows are fully accessible via keyboard

## Coding Standards

### General

**if/else if/else:**

While there are instances of `else if` and `else` statements in the current code base. We are constantly refactoring them as we find them. In general, unless absolutely necessary which is very rare, don't use `else` and especially not long chains of `else if`.

Instead, use guard clause (w/ early returns) or switch statements.

### Go (Backend)

- Follow standard Go conventions and `gofmt`
- Use meaningful variable and function names
- Add comments for exported functions
- Handle errors explicitly (no silent failures)
- Use structured logging with zerolog

```go
// Good
func (s *Store) GetMessageByID(id string) (*Message, error) {
    if id == "" {
        return nil, fmt.Errorf("message ID is required")
    }
    // ...
}

// Avoid
func (s *Store) Get(x string) *Message {
    // ...
}
```

### TypeScript/Svelte (Frontend)

- Use TypeScript for type safety
- Follow Svelte 5 patterns (runes, `$state`, `$derived`)
- Keep components focused and under 500 lines
- Use meaningful component and variable names

```svelte
<!-- Good -->
<script lang="ts">
  let { message, onDelete }: { message: Message; onDelete: () => void } = $props()
  let isExpanded = $state(false)
</script>

<!-- Avoid -->
<script>
  export let m
  let x = false
</script>
```

### Commit Messages

Use clear, descriptive commit messages:

```
feat: Add keyboard shortcut for archive (Ctrl+E)

- Added handler in App.svelte
- Updated keyboard.svelte.ts store
- Added tooltip to archive button

Closes #123
```

Prefixes:
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation changes
- `style:` Code style changes (formatting, etc.)
- `refactor:` Code refactoring
- `test:` Adding or updating tests
- `chore:` Maintenance tasks


## Testing

### Running Tests

```bash
# Go tests
go test ./...

# Frontend tests (if available)
cd frontend && npm test
```

### Writing Tests

- Write tests for new functionality
- Focus on critical paths and edge cases
- Use table-driven tests in Go where appropriate


## Getting Started

### Prerequisites

- **mise** - multiplatform tool version manager (installs Go and Node.js automatically)
- **Git**

Install mise: https://mise.jdx.dev

### Development Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/hkdb/aerion.git
   cd aerion
   ```

2. **Install dependencies** (Go, Node.js, Wails, and frontend packages)
   ```bash
   make setup
   ```

3. **Set up environment variables**
   ```bash
   cp .env.example .env
   # Edit .env with your OAuth credentials (for Gmail/OAuth testing)
   ```

4. **Run in development mode**
   ```bash
   make dev
   ```

### Building

```bash
make build
```
Local Flatpak test build:

```bash
make flatpak-dev
```
Test Flatpak production build:

```bash
make flatpak
```

## Questions?

- Open an [Issue](https://github.com/hkdb/aerion/issues) with the `Question` template for questions
- Check existing issues first
- Be patient - maintainers are volunteers

## License

By contributing to Aerion, you agree that your contributions will be licensed under the Apache License 2.0.
