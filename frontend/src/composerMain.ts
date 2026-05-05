import { mount } from "svelte";

import ComposerApp from "./ComposerApp.svelte";
import "./app.css";
import { initI18n } from "./lib/i18n";

// Initialize i18n and wait for locale to load before mounting
// ($_ throws if called before the locale is ready)
async function bootstrap() {
  await initI18n();
  mount(ComposerApp, { target: document.getElementById("app")! });
}

bootstrap();
