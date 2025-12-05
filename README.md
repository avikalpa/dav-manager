# dav-manager

Single-binary CLI (Go) to keep a Radicale address book clean, normalized, and easy to reclaim. It covers CRUD, phone/email normalization, “un-contacts” buckets, photo handling, markdown reconciliation, and client-refresh helpers so DAVx5/WhatsApp pick up changes reliably.

## Why
- Normalize names/phones/emails and keep structured `N` aligned with `FN`.
- Bucket “not now” contacts into folders (psychology, corporate, lost-in-time, neutral, email_only, etc.) with VCF backups.
- Force stubborn clients to refresh with REV bumps and fresh UIDs.
- Scriptable flows to audit or “spring clean” your address book.

## Quick start
1) Copy `.env.example` → `.env` and fill:
   - `RADICALE_BASE_URL` `RADICALE_COLLECTION` `RADICALE_USER` `RADICALE_PASS`
   - `UN_CONTACTS` (e.g. `/home/pi/data/smbfs/dada/un-contacts`)
   - `PHOTO_MAP` (default `photo-map.json`), `ENABLE_GRAVATAR=0|1`
2) Build: `go build -o bin/dav-helper ./...` (binary is gitignored)
3) List live contacts: `bin/dav-helper fetch`
4) Bucket view: `bin/dav-helper fetch --un-contacts`
5) Force client refresh: `bin/dav-helper fetch --touch-all`

## Core commands
- Add: `bin/dav-helper add --name "Jane Doe" --emails jane@example.com --phones "+1 4803957551" --note "Friend"`
- Update: `bin/dav-helper update --name "Jane Doe" --new-name "Jane D." --phones "+1 4803957551,+91 9876543210"`
- Delete with backup: `bin/dav-helper delete --name "Noise Lead" --vcf "$UN_CONTACTS/psychology/noise-lead.vcf"`
- Move to bucket: `bin/dav-helper move --name "Vendor X" --bucket corporate --new-name "Vendor X (2019)"`
- Sync from markdown: `bin/dav-helper sync --source docs/examples/example-table.md --apply --touch`
  - Extras go to `UN_CONTACTS/neutral`
  - Phones normalized (non-+91 first), emails lowercased, `N` kept in sync with `FN`
- Photos: `bin/dav-helper photos --apply --map photo-map.json --gravatar`
- Bucket hygiene: `bin/dav-helper clean-buckets --apply`
- Name fix: `bin/dav-helper fix-names --apply` (sets structured `N=FN` everywhere)
- UID refresh: `bin/dav-helper refresh-uids --apply` (recreate cards with new UIDs/hrefs)

## Buckets (“un-contacts”)
- Structured folders under `UN_CONTACTS` (e.g. `psychology/`, `corporate/`, `lost-in-time/`, `neutral/`, `email_only/`).
- Each entry is a VCF file with legible filenames; multi-card VCFs are merged and normalized.
- `fetch --un-contacts` prints a grouped table; `clean-buckets --apply` normalizes phone order/format.
- Delete/move commands always create a VCF backup; if `--vcf` is omitted, the backup path is printed for you.

## Photos
- Provide a map in `photo-map.json`: `{ "Jane Doe": "/abs/path/jane.jpg" }`
- Enable Gravatar by setting `ENABLE_GRAVATAR=1`.
- Run `photos --apply` to embed.

## Workflows and examples
- See `docs/WORKFLOWS.md` for daily hygiene, bucket maintenance, and “reclaim the life” loops.
- See `docs/examples/example-table.md` for a markdown table suitable for `sync`.

## Standards
- Phones: E.164-ish with spacing; non-+91 numbers appear first; duplicates removed.
- Emails: lowercased/deduped.
- Names: title-style, structured `N` kept in sync with display name.

## Contributing
- Go 1.22+, no Python dependency.
- Run `gofmt` before sending patches.
- Add new workflows/examples under `docs/` to keep the project self-explanatory.
