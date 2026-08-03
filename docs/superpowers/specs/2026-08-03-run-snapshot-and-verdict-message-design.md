# Run Snapshot and Verdict Message Design

Each run already contains its submitted `RunConfig`; the UI must render all result detail from that run object, never from the current form. Automatic-search summary messages will include the first failed run's measured total, error rate, P95, and configured thresholds. The UI will describe the specific breach, preferring zero completed requests, then error rate breach, then P95 breach.

Model preset maximum tokens change to 1,280 for Short response, 20,480 for Coding task, and 163,840 for Long output. Presets continue to change only prompt and max tokens. Tests cover message selection, snapshot rendering inputs, and all preset values.
