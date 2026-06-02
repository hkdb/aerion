package backend

// Bridge-level tests previously covered the translateConsentRequired helper
// and the findSourceForAccount lookup. Both helpers were removed when the
// extension switched to the WriteAccessAccountPicker flow (the frontend no
// longer parses a JSON-encoded consent sentinel; the bridge no longer
// auto-resolves a source from an account ID). Their tests are gone with
// them. This file is intentionally kept as a placeholder so the package
// still has a test target — new bridge tests should be added here.
