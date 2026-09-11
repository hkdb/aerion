# Custom CSS

Aerion loads an optional `custom.css` file from its platform-specific configuration directory. The file is applied after built-in styles and reloaded automatically when it is created, changed, replaced, or removed.

The stylesheet affects both the main window and detached composer windows. In the current implementation, the presence of the file activates the stylesheet, and no settings option is required.

## Location

| Platform | Default path |
| --- | --- |
| Linux | `~/.config/aerion/custom.css` |
| Linux (Flatpak) | `~/.var/app/io.github.hkdb.Aerion/config/aerion/custom.css` |
| macOS | `~/Library/Application Support/Aerion/custom.css` |
| Windows | `%APPDATA%\Aerion\custom.css` |

On Linux, `$XDG_CONFIG_HOME` replaces `~/.config` when set. Aerion always resolves the file through its platform configuration directory rather than a hard-coded path.

`custom.css` may contain CSS variable overrides, ordinary selectors, or both:

```css
:root {
  color-scheme: dark;
  --background: 220 15% 12%;
  --foreground: 220 10% 92%;
  --primary: 190 80% 55%;
  --primary-foreground: 220 15% 12%;
  --border: 220 10% 25%;
  --ring: 190 80% 55%;
}

body {
  font-size: 15px;
}
```

Theme color values use HSL components without the surrounding `hsl()` function.

## Theme variables

A complete theme may override these core variables:

```css
:root {
  color-scheme: dark;
  --background: 0 0% 0%;
  --foreground: 0 0% 100%;
  --card: 0 0% 0%;
  --card-foreground: 0 0% 100%;
  --popover: 0 0% 0%;
  --popover-foreground: 0 0% 100%;
  --primary: 0 0% 100%;
  --primary-foreground: 0 0% 0%;
  --secondary: 0 0% 15%;
  --secondary-foreground: 0 0% 100%;
  --muted: 0 0% 15%;
  --muted-foreground: 0 0% 65%;
  --accent: 0 0% 15%;
  --accent-foreground: 0 0% 100%;
  --destructive: 0 70% 45%;
  --destructive-foreground: 0 0% 100%;
  --border: 0 0% 25%;
  --input: 0 0% 25%;
  --ring: 0 0% 80%;
  --radius: 0.5rem;
  --dark-mail-bg-l: 10;
}
```

The optional avatar palette uses `--avatar-fg` and `--avatar-1` through `--avatar-14`.

Partial overrides inherit all unspecified values from the currently selected built-in theme. Switching built-in themes does not disable or remove `custom.css`. The current implementation always applies the stylesheet as an override layer; the final activation policy remains to be decided in issue #342.

## Live reload

Aerion watches the containing configuration directory, so both direct writes and atomic replacement patterns are supported:

```bash
cp generated.css ~/.config/aerion/custom.css.tmp
mv ~/.config/aerion/custom.css.tmp ~/.config/aerion/custom.css
```

Deleting or emptying the file clears the overrides and reveals the selected built-in theme again. The maximum supported file size is 1 MiB.

## Security

Custom CSS is loaded as application UI styling and may reference resources supported by the embedded browser. Only use stylesheets you trust. Aerion does not execute commands or JavaScript from this file.
