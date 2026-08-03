export function loadSavedValue(storage, key, fallback) {
  try {
    const value = storage.getItem(key);
    if (value == null) return fallback;
    const parsed = JSON.parse(value);
    return fallback && typeof fallback === "object" && !Array.isArray(fallback) ? { ...fallback, ...parsed } : parsed;
  } catch {
    return fallback;
  }
}

export function saveValue(storage, key, value) {
  try {
    storage.setItem(key, JSON.stringify(value));
  } catch {
    // Browsers can disable storage; the form still works for the current session.
  }
}
