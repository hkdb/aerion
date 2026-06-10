//go:build linux

package platform

/*
#cgo !webkit2_41 pkg-config: webkit2gtk-4.0 glib-2.0
#cgo webkit2_41 pkg-config: webkit2gtk-4.1 glib-2.0
#include <webkit2/webkit2.h>
#include <glib.h>

static gboolean enable_spellcheck_cb(gpointer data) {
WebKitWebContext *ctx = webkit_web_context_get_default();
webkit_web_context_set_spell_checking_enabled(ctx, TRUE);

const char *lang = g_getenv("LANG");
if (lang == NULL || lang[0] == 0) {
lang = "en_US";
}
gchar **parts = g_strsplit(lang, ".", 2);
const gchar *langs[] = { parts[0], NULL };
webkit_web_context_set_spell_checking_languages(ctx, langs);
g_strfreev(parts);

return G_SOURCE_REMOVE;
}

static void schedule_enable_spellcheck() {
g_idle_add(enable_spellcheck_cb, NULL);
}
*/
import "C"

// EnableSpellChecking enables spell checking on the default WebKit web context
// using the system enchant/hunspell dictionaries. Language is derived from
// the LANG environment variable. Dispatched to the GTK main thread.
func EnableSpellChecking() {
	C.schedule_enable_spellcheck()
}
