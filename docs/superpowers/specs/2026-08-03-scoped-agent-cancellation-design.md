# Scoped Agent Cancellation Design

Cancelling an automatic search marks only its own queued or running Load Observatory runs as cancelled. The Agent polls the Controller for the specific run it claimed; when that run becomes cancelled it cancels only the context passed to its own worker HTTP requests. No request is sent to the model/web target to stop it, and unrelated external traffic or other Observatory runs are not affected.
