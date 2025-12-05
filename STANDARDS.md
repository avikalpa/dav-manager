# Contact Standards

These conventions drive automated cleanup, dedupe, and bucketing of the Radicale address book.

## Buckets (pruned contacts only)
- `UN_CONTACTS=/home/pi/data/smbfs/dada/un-contacts` holds pruned/rejected cards only; it is not the primary storage.
- Buckets live directly under `UN_CONTACTS`: `psychology/` (upsetting contacts), `neutral/` (incomplete/unknown), `archive/` (old or low-touch official contacts). Add more buckets as peer subfolders as needed.
- Do not tag the main address book; buckets are only for storing removed vcards.

## Names
- Display name (`FN`): Title Case “Given Family”; no honorifics. Nicknames/context go in `NOTE`. If `FN` is just an email/handle, keep it as-is (lowercase).
- Structural name (`N`): family, given, additional, prefix, suffix.
- Filenames/UIDs: lower-kebab (`given-family-<shortid>.vcf`), keeping existing UIDs unless recreated.

## Phones
- Format E.164 with spaces for readability (e.g., `+91 98765 43210`).
- Labels: `cell`, `work`, `home`, `fax`, `other`. Prefer one primary per label.

## Email
- Lowercase all addresses. Primary first; demote stale/legacy emails to `NOTE` or remove if fully dead.

## Addresses
- Keep structured `ADR` fields (city/state/postcode/country). Put directions/landmarks in `NOTE`.

## Notes/Metadata
- Use `NOTE` for context: how you know them, boundaries, source, legacy numbers/emails.
- Preserve `UID` and `REV` when editing in place. Generate new `REV` timestamps when changing cards.

## Required minimum per contact
- Must have at least one of: phone, email, or address. Otherwise move to `neutral/` until completed.

## Collections and tags
- Core address book remains in Radicale. Buckets are mirrored locally (and optionally in Radicale collections named after buckets if desired later).
- Tags within a card (CATEGORIES) may be added to reflect bucket names for quick filtering.

## Automation defaults
- Scripts run in dry-run by default; explicit `--apply` to write.
- Credentials pulled from environment variables; never committed.
- Logs include changes, moves, and detected duplicates.
