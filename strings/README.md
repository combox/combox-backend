# Backend Strings

All runtime response texts must come from files in this directory.

Rules:

- one file per locale (`<locale>.json`)
- flat JSON dictionary (`key: value`)
- key format: `group.name`
- `en.json` is required as fallback

Current keys:

- `service.name`
- `status.ok`
- `status.running`
- `status.up`
- `status.down`
- `status.degraded`
- `check.postgres`
- `check.valkey`
