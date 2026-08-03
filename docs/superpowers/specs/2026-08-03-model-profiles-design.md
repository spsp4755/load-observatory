# Model profile design

Users save a reusable model profile containing a display name, OpenAI-compatible endpoint, model name, and optional API key. The Controller persists it in the existing store and redacts the key from list and create responses. Only an Agent assignment receives the real key, which it sends as an Authorization bearer header.

The Test page lists saved profiles, allows selection to fill the target fields, and supports registration and deletion. Direct entry remains available. No browser storage or third-party secret manager is added; PostgreSQL is the existing closed-network persistence boundary.

Tests cover redaction, validation, Agent header propagation, and form conversion. The UI is checked in the local browser.
