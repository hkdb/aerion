<script lang="ts">
  import { _ } from "$lib/i18n";
  import Icon from "@iconify/svelte";
  import type { Editor } from "@tiptap/core";
  import { onDestroy, onMount } from "svelte";

  interface Props {
    editor: Editor | null;
    isPlainTextMode?: boolean;
    onTogglePlainText?: () => void;
    onInsertImage?: () => void;
  }

  let {
    editor,
    isPlainTextMode = false,
    onTogglePlainText,
    onInsertImage
  }: Props = $props();

  // Hint mode state (Alt+T shows numbered hints on buttons)
  let hintMode = $state(false);
  let toolbarRef = $state<HTMLElement | null>(null);

  // Generate hint keys: 1-9, then a-z
  const hintKeys = [
    "1",
    "2",
    "3",
    "4",
    "5",
    "6",
    "7",
    "8",
    "9",
    "a",
    "b",
    "c",
    "d",
    "e",
    "f",
    "g",
    "h",
    "i",
    "j",
    "k",
    "l",
    "m",
    "n",
    "o",
    "p",
    "q",
    "r",
    "s",
    "t",
    "u",
    "v",
    "w",
    "x",
    "y",
    "z"
  ];

  // Toggle hint mode (called via Alt+T from parent)
  export function focus() {
    hintMode = !hintMode;
  }

  // Get all enabled buttons in the toolbar
  function getToolbarButtons(): HTMLButtonElement[] {
    if (!toolbarRef) return [];
    return Array.from(
      toolbarRef.querySelectorAll("button:not([disabled])")
    ) as HTMLButtonElement[];
  }

  // Handle hint key press
  function handleHintKeydown(e: KeyboardEvent) {
    if (!hintMode) return;

    if (e.key === "Escape") {
      e.preventDefault();
      hintMode = false;
      return;
    }

    const key = e.key.toLowerCase();
    const hintIndex = hintKeys.indexOf(key);
    if (hintIndex === -1) return;

    const buttons = getToolbarButtons();
    if (hintIndex < buttons.length) {
      e.preventDefault();
      buttons[hintIndex].click();
      hintMode = false;
    }
  }

  // Close hint mode when clicking outside
  function handleClickOutsideHints(e: MouseEvent) {
    if (hintMode) {
      hintMode = false;
    }
  }

  // Track active formatting states - only updated on transaction
  let activeStates = $state({
    bold: false,
    italic: false,
    underline: false,
    strike: false,
    bulletList: false,
    orderedList: false,
    blockquote: false,
    link: false
  });

  // Color and font size state
  let currentColor = $state<string | null>(null);
  let showColorPicker = $state(false);
  let currentFontSize = $state<string>("");
  let showFontSizePicker = $state(false);
  let currentAlign = $state<"left" | "center" | "right">("left");

  // Preset colors
  const presetColors = [
    "#000000",
    "#374151",
    "#6b7280",
    "#dc2626",
    "#ea580c",
    "#ca8a04",
    "#16a34a",
    "#0891b2",
    "#2563eb",
    "#7c3aed",
    "#c026d3",
    "#e11d48"
  ];

  // Font sizes
  const fontSizes = [
    "10px",
    "12px",
    "14px",
    "16px",
    "18px",
    "20px",
    "24px",
    "28px",
    "32px"
  ];

  // Update active states from editor
  function updateActiveStates() {
    if (!editor) return;

    activeStates = {
      bold: editor.isActive("bold"),
      italic: editor.isActive("italic"),
      underline: editor.isActive("underline"),
      strike: editor.isActive("strike"),
      bulletList: editor.isActive("bulletList"),
      orderedList: editor.isActive("orderedList"),
      blockquote: editor.isActive("blockquote"),
      link: editor.isActive("link")
    };

    // Get current color
    const colorAttr = editor.getAttributes("textStyle").color;
    currentColor = colorAttr || null;

    // Get current font size
    const fontSizeAttr = editor.getAttributes("textStyle").fontSize;
    currentFontSize = fontSizeAttr || "";

    // Get current alignment
    if (editor.isActive({ textAlign: "center" })) {
      currentAlign = "center";
    } else if (editor.isActive({ textAlign: "right" })) {
      currentAlign = "right";
    } else {
      currentAlign = "left";
    }
  }

  // Subscribe to editor transactions to update button states
  // This is more efficient than checking isActive() on every render
  $effect(() => {
    if (!editor) return;

    // Initial state
    updateActiveStates();

    // Listen for selection/content changes
    const handleTransaction = () => {
      updateActiveStates();
    };

    editor.on("transaction", handleTransaction);

    return () => {
      editor.off("transaction", handleTransaction);
    };
  });

  // Toolbar actions
  function toggleBold() {
    editor?.chain().focus().toggleBold().run();
  }

  function toggleItalic() {
    editor?.chain().focus().toggleItalic().run();
  }

  function toggleUnderline() {
    editor?.chain().focus().toggleUnderline().run();
  }

  function toggleStrike() {
    editor?.chain().focus().toggleStrike().run();
  }

  function toggleBulletList() {
    editor?.chain().focus().toggleBulletList().run();
  }

  function toggleOrderedList() {
    editor?.chain().focus().toggleOrderedList().run();
  }

  function toggleBlockquote() {
    editor?.chain().focus().toggleBlockquote().run();
  }

  function insertLink() {
    const url = prompt("Enter URL:");
    if (url) {
      editor?.chain().focus().setLink({ href: url }).run();
    }
  }

  // Color functions
  function setColor(color: string) {
    editor?.chain().focus().setColor(color).run();
    showColorPicker = false;
  }

  function removeColor() {
    editor?.chain().focus().unsetColor().run();
    showColorPicker = false;
  }

  function handleCustomColor(event: Event) {
    const input = event.target as HTMLInputElement;
    setColor(input.value);
  }

  function toggleColorPicker() {
    showColorPicker = !showColorPicker;
    showFontSizePicker = false;
  }

  // Font size functions
  function setFontSize(size: string) {
    editor?.chain().focus().setFontSize(size).run();
    showFontSizePicker = false;
  }

  function toggleFontSizePicker() {
    showFontSizePicker = !showFontSizePicker;
    showColorPicker = false;
  }

  // Alignment functions
  function setAlign(align: "left" | "center" | "right") {
    editor?.chain().focus().setTextAlign(align).run();
  }

  // Close pickers when clicking outside
  function handleClickOutside(event: MouseEvent) {
    const target = event.target as HTMLElement;
    if (!target.closest(".color-picker-container")) {
      showColorPicker = false;
    }
    if (!target.closest(".font-size-picker-container")) {
      showFontSizePicker = false;
    }
  }
</script>

<svelte:window
  onkeydown={handleHintKeydown}
  onclick={handleClickOutsideHints}
/>

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div
  bind:this={toolbarRef}
  class="gap-1 px-4 py-2 border-border relative flex items-center border-b"
  role="toolbar"
  aria-label={$_("aria.textFormatting")}
  tabindex="-1"
  onclick={handleClickOutside}
>
  <!-- Hint mode overlay -->
  {#if hintMode}
    <div
      class="inset-0 bg-background/50 pointer-events-none absolute z-10 backdrop-blur-[1px]"
    ></div>
  {/if}

  <div class="relative">
    <button
      onclick={toggleBold}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.bold}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.bold")}
    >
      <Icon icon="mdi:format-bold" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >1</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={toggleItalic}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.italic}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.italic")}
    >
      <Icon icon="mdi:format-italic" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >2</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={toggleUnderline}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.underline}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.underline")}
    >
      <Icon icon="mdi:format-underline" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >3</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={toggleStrike}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.strike}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.strikethrough")}
    >
      <Icon icon="mdi:format-strikethrough" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >4</span
      >
    {/if}
  </div>

  <div class="h-5 bg-border mx-1 w-px"></div>

  <!-- Color Picker -->
  <div
    class="color-picker-container relative"
    role="presentation"
    onclick={(e) => e.stopPropagation()}
  >
    <button
      onclick={toggleColorPicker}
      class="p-1.5 rounded hover:bg-muted gap-0.5 flex items-center transition-colors"
      class:bg-muted={showColorPicker}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.textColor")}
    >
      <Icon icon="mdi:format-color-text" class="w-5 h-5" />
      <div
        class="w-4 h-1 rounded-sm"
        style="background-color: {currentColor || '#000000'}"
      ></div>
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >5</span
      >
    {/if}

    {#if showColorPicker && !isPlainTextMode}
      <div
        class="left-0 mt-1 p-2 bg-popover border-border rounded-md shadow-lg absolute top-full z-50 border"
      >
        <div class="gap-1 mb-2 grid grid-cols-4">
          {#each presetColors as color}
            <button
              onclick={() => setColor(color)}
              class="w-6 h-6 rounded border-border border transition-transform hover:scale-110"
              style="background-color: {color}"
              title={color}
            ></button>
          {/each}
        </div>
        <div class="gap-2 pt-2 border-border flex items-center border-t">
          <input
            type="color"
            value={currentColor || "#000000"}
            onchange={handleCustomColor}
            class="w-6 h-6 rounded cursor-pointer"
            title={$_("editor.customColor")}
          />
          <button
            onclick={removeColor}
            class="text-xs text-muted-foreground hover:text-foreground"
          >
            {$_("editor.reset")}
          </button>
        </div>
      </div>
    {/if}
  </div>

  <!-- Font Size Picker -->
  <div
    class="font-size-picker-container relative"
    role="presentation"
    onclick={(e) => e.stopPropagation()}
  >
    <button
      onclick={toggleFontSizePicker}
      class="p-1.5 rounded hover:bg-muted gap-0.5 text-xs flex min-w-[40px] items-center justify-center transition-colors"
      class:bg-muted={showFontSizePicker}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.fontSize")}
    >
      {currentFontSize || "14px"}
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >6</span
      >
    {/if}

    {#if showFontSizePicker && !isPlainTextMode}
      <div
        class="left-0 mt-1 py-1 bg-popover border-border rounded-md shadow-lg absolute top-full z-50 min-w-[60px] border"
      >
        {#each fontSizes as size}
          <button
            onclick={() => setFontSize(size)}
            class="px-3 py-1 text-sm hover:bg-muted w-full text-left transition-colors"
            class:bg-muted={currentFontSize === size}
          >
            {size}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <div class="h-5 bg-border mx-1 w-px"></div>

  <!-- Alignment buttons -->
  <div class="relative">
    <button
      onclick={() => setAlign("left")}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={currentAlign === "left"}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.alignLeft")}
    >
      <Icon icon="mdi:format-align-left" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >7</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={() => setAlign("center")}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={currentAlign === "center"}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.alignCenter")}
    >
      <Icon icon="mdi:format-align-center" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >8</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={() => setAlign("right")}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={currentAlign === "right"}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.alignRight")}
    >
      <Icon icon="mdi:format-align-right" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >9</span
      >
    {/if}
  </div>

  <div class="h-5 bg-border mx-1 w-px"></div>

  <div class="relative">
    <button
      onclick={toggleBulletList}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.bulletList}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.bulletList")}
    >
      <Icon icon="mdi:format-list-bulleted" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >a</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={toggleOrderedList}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.orderedList}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.numberedList")}
    >
      <Icon icon="mdi:format-list-numbered" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >b</span
      >
    {/if}
  </div>
  <div class="h-5 bg-border mx-1 w-px"></div>
  <div class="relative">
    <button
      onclick={toggleBlockquote}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.blockquote}
      class:opacity-50={isPlainTextMode}
      disabled={isPlainTextMode}
      tabindex="-1"
      title={$_("editor.quote")}
    >
      <Icon icon="mdi:format-quote-close" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >c</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={insertLink}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      class:bg-muted={activeStates.link}
      title={$_("editor.insertLink")}
      disabled={isPlainTextMode}
      class:opacity-50={isPlainTextMode}
      tabindex="-1"
    >
      <Icon icon="mdi:link" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >d</span
      >
    {/if}
  </div>
  <div class="relative">
    <button
      onclick={onInsertImage}
      class="p-1.5 rounded hover:bg-muted transition-colors"
      title={$_("editor.insertImage")}
      disabled={isPlainTextMode}
      class:opacity-50={isPlainTextMode}
      tabindex="-1"
    >
      <Icon icon="mdi:image" class="w-5 h-5" />
    </button>
    {#if hintMode && !isPlainTextMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >e</span
      >
    {/if}
  </div>

  <!-- Spacer -->
  <div class="flex-1"></div>

  <!-- Plain text toggle -->
  <div class="relative">
    <button
      onclick={onTogglePlainText}
      class="p-1.5 rounded hover:bg-muted gap-1.5 text-xs flex items-center transition-colors"
      class:bg-muted={isPlainTextMode}
      tabindex="-1"
      title={isPlainTextMode
        ? $_("editor.switchToRichText")
        : $_("editor.switchToPlainText")}
    >
      <Icon
        icon={isPlainTextMode ? "mdi:format-text" : "mdi:text"}
        class="w-5 h-5"
      />
      <span class="sm:inline hidden"
        >{isPlainTextMode
          ? $_("editor.richText")
          : $_("editor.plainText")}</span
      >
    </button>
    {#if hintMode}
      <span
        class="-top-1 -left-1 bg-primary text-primary-foreground font-bold w-4 h-4 rounded absolute z-20 flex items-center justify-center text-[10px]"
        >f</span
      >
    {/if}
  </div>
</div>
