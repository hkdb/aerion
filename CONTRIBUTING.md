# Contributing to Aerion

Thank you for your interest in contributing to Aerion! This document provides guidelines and instructions for contributing.

## Code of Conduct

By participating in this project, you agree to maintain a respectful and inclusive environment for everyone.

## Getting Started

### Prerequisites

- **Go** 1.21 or later
- **Node.js** 18 or later
- **Wails** v2 CLI
- **Git**

### Development Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/hkdb/aerion.git
   cd aerion
   ```

2. **Install Go dependencies**
   ```bash
   go mod download
   ```

3. **Install frontend dependencies**
   ```bash
   cd frontend
   npm install
   cd ..
   ```

4. **Set up environment variables**
   ```bash
   cp .env.example .env
   # Edit .env with your OAuth credentials (for Gmail/OAuth testing)
   ```

5. **Run in development mode**
   ```bash
   make dev
   ```

### Building

```bash
make build
```

## How to Contribute

### Reporting Bugs

1. Check if the bug has already been reported in [Issues](https://github.com/hkdb/aerion/issues)
2. If not, create a new issue with:
   - Clear, descriptive title
   - Steps to reproduce
   - Expected vs actual behavior
   - Environment details (OS, version, etc.)
   - Relevant logs or screenshots

### Suggesting Features

1. Check existing issues for similar suggestions
2. Create a new issue with the `enhancement` label
3. Describe the feature and its use case
4. Be open to discussion and feedback

### Pull Requests

1. **Fork the repository** and create your branch from `main`
2. **Make your changes** following our coding standards
3. **Test your changes** thoroughly
4. **Commit with clear messages** (see commit guidelines below)
5. **Push to your fork** and submit a pull request

## Coding Standards

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

## Project Structure

```
aerion/
├── app/                # Main Wails application logic
│   ├── app.go          # Main app structure
│   ├── mailto.go       # Mailto URL parsing
│   └── ...
├── internal/           # Internal Go packages
│   ├── account/        # Account management
│   ├── imap/           # IMAP client and connection pool
│   ├── sync/           # Email sync engine
│   ├── message/        # Message storage
│   └── ...
├── frontend/           # Svelte frontend
│   ├── src/
│   │   ├── App.svelte  # Main app component
│   │   ├── lib/
│   │   │   ├── components/  # UI components
│   │   │   ├── stores/      # Svelte stores
│   │   │   └── ui/          # Design system
│   │   └── ...
│   └── ...
└── ...
```

## Testing

### Running Tests

```bash
# Go tests (including unit tests for internal/ and app/)
go test ./...

# Run a specific package test
go test -v ./app
```

### Writing Tests

- Write tests for new functionality
- Focus on critical paths and edge cases
- Use table-driven tests in Go where appropriate

## Areas for Contribution

We welcome contributions in these areas:

- **Bug fixes** - Check issues labeled `bug`
- **Documentation** - README, code comments, guides
- **Tests** - We need more test coverage
- **Accessibility** - Improving keyboard navigation and screen reader support
- **Performance** - Optimization opportunities
- **Features** - Check issues labeled `enhancement`

## Questions?

- Open a [Discussion](https://github.com/hkdb/aerion/discussions) for questions
- Check existing issues and discussions first
- Be patient - maintainers are volunteers

## License

By contributing to Aerion, you agree that your contributions will be licensed under the Apache License 2.0.
