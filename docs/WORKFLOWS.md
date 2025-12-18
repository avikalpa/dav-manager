# Workflows and Examples

## Purpose
These workflows show how to reclaim a messy address book with repeatable, scriptable steps. The CLI keeps Radicale clean, enforces naming/phone standards, and buckets “un-contacts” into folders you can revisit later.

## Environment setup
- Copy `.env.example` to `.env` and fill `RADICALE_USER`, `RADICALE_PASS`, `RADICALE_BASE_URL`, `RADICALE_COLLECTION`, `UN_CONTACTS`, `PHOTO_MAP`.
- Build: `go build -o bin/dav ./...`

## Daily hygiene (live address book)
- Inspect everything: `bin/dav contacts fetch`
- Force clients to refresh names/phones (REV bump): `bin/dav contacts fetch --touch-all`
- Add/update:
  - `bin/dav contacts add --name "Jane Doe" --emails jane@example.com --phones "+1 4803957551" --note "Friend"`
  - `bin/dav contacts update --name "Jane Doe" --new-name "Jane D." --phones "+1 4803957551,+91 9876543210"`
- Delete with safety net:
  - `bin/dav contacts delete --name "Noise Lead" --vcf "$UN_CONTACTS/psychology/noise-lead.vcf"`
  - If you omit `--vcf`, the CLI writes a backup in the current directory and tells you the path.
- Photos:
  - Local map: add entries to `photo-map.json` such as `"Jane Doe": "/abs/path/jane.jpg"`
  - Gravatar fallback: set `ENABLE_GRAVATAR=1`, then `bin/dav contacts photos --apply`

## Bucket hygiene (“un-contacts”)
- List buckets: `bin/dav contacts fetch --un-contacts`
- Move a server contact into a bucket (renaming optional):
  - `bin/dav contacts move --name "Old Vendor" --bucket corporate --new-name "Old Vendor (2019)"`
- Restore a bucketed contact back into the server:
  - `bin/dav contacts restore --name "Old Vendor (2019)" --bucket corporate`
- Clean bucket numbers (ordering, E.164-ish): `bin/dav contacts clean-buckets --apply`
- Keep names legible: filenames are normalized automatically; edit the VCF `FN` if you want a different display.

## Reconciliation from markdown
For audit-style edits, keep a markdown table (see `docs/examples/example-table.md`). Then:
```
bin/dav contacts sync --source docs/examples/example-table.md --apply --touch
```
- Any contact not present in the table is backed up to `UN_CONTACTS/neutral`.
- Phone numbers are normalized; non-+91 numbers become primary.
- Structured `N` is kept in sync with `FN` to satisfy Android/DAVx5/WhatsApp.

## Forcing mobile refreshes
If a client refuses to pick up renamed contacts:
- `bin/dav contacts fix-names --apply`   # sets structured N=FN for all
- `bin/dav contacts refresh-uids --apply` # recreates cards with fresh UIDs/hrefs
- Then re-sync DAVx5/WhatsApp.

## Example “reclaim the life” loop
1) `bin/dav contacts fetch` and skim the table.
2) Move junk to buckets with `move` (psychology/corporate/lost-in-time/etc.).
3) Normalize/merge via `update` or a markdown pass + `sync --apply --touch`.
4) Add photos (`photos --apply`) and force refresh (`fix-names`, `refresh-uids`, `fetch --touch-all`).
5) Periodically run `clean-buckets --apply` to keep the buckets tidy.
