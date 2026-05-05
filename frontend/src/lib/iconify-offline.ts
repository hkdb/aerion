// Configure Iconify to work offline with bundled icon data
import heroiconsIcons from "@iconify-json/heroicons/icons.json";
import logosIcons from "@iconify-json/logos/icons.json";
import lucideIcons from "@iconify-json/lucide/icons.json";
// Import icon data from installed packages
import mdiIcons from "@iconify-json/mdi/icons.json";
import simpleIcons from "@iconify-json/simple-icons/icons.json";
import { addCollection } from "@iconify/svelte";

// Add all icon collections
addCollection(mdiIcons);
addCollection(lucideIcons);
addCollection(heroiconsIcons);
addCollection(logosIcons);
addCollection(simpleIcons);
