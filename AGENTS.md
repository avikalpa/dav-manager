# AGENTS

## Purpose
Coordinate small agents/scripts that maintain and polish the Radicale address book while respecting user intent and safety.

## Roles
- **Human (owner)**: approves standards, naming, and bucket definitions; supplies credentials and pruned contact folders.
- **Contact curator agent**: fetches contacts from Radicale, normalizes names/phones/emails per STANDARDS, and re-uploads.
- **Bucket classifier agent**: when removing a contact from Radicale, stores its vcard under the appropriate bucket (e.g., psychology/, neutral/, archive/, plus future buckets) in `UN_CONTACTS` and keeps a log of moves.
- **Quality/sanity agent**: detects duplicates, incomplete entries, suspicious data, and prepares review diffs before applying.

## Data sources
- Radicale server (address book).
- Local pruned contacts (removed from Radicale): set `UN_CONTACTS=/home/pi/data/smbfs/dada/un-contacts`.

## Operating notes
- Never persist secrets in the repo; read credentials from env or config outside git.
- Work in branches and keep change logs for every sync/cleanup run.
- Run idempotent scripts; make dry-run mode the default.

## Releases (GitHub Actions)
- Push a semver tag (`vX.Y.Z`) to publish a GitHub Release with attached binaries for Linux/macOS/Windows (amd64/arm64).
- The workflow is `.github/workflows/release.yml`; it auto-generates release notes and uploads the built artifacts.
