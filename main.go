package main

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	vcard "github.com/emersion/go-vcard"
)

// Environment variables:
// RADICALE_BASE_URL (default: https://dav.gour.top/)
// RADICALE_COLLECTION (default: /dada/cf1c3fea-c8f4-02ab-efd3-bcc226e6b7cc/)
// RADICALE_USER / RADICALE_PASS
// UN_CONTACTS (default: /home/pi/data/smbfs/dada/un-contacts)
// PHOTO_MAP (default: photo-map.json), ENABLE_GRAVATAR (default: 0)

type cardRef struct {
	Href string
	ETag string
}

type cardData struct {
	Ref  cardRef
	Card vcard.Card
}

type desiredEntry struct {
	Name   string
	Emails []string
	Phones []string
	Note   string
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "contacts":
		contactsMain(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		usage()
	}
}

func usage() {
	fmt.Println("Usage: dav <domain> <command> [options]")
	fmt.Println("Domains:")
	fmt.Println("  contacts   manage CardDAV contacts (fetch/add/update/delete/move/sync/etc.)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  dav contacts fetch")
	fmt.Println("  dav contacts fetch --un-contacts")
	fmt.Println("  dav contacts add --name \"Jane Doe\" --emails jane@example.com --phones \"+1 4803957551\"")
	fmt.Println("  dav contacts delete --name \"Old Lead\" --vcf \"$UN_CONTACTS/psychology/old-lead.vcf\"")
	fmt.Println("  dav contacts sync --source docs/examples/example-table.md --apply --touch")
}

func contactsMain(args []string) {
	if len(args) == 0 {
		contactsUsage()
		return
	}
	switch args[0] {
	case "fetch":
		fetchCmd := flag.NewFlagSet("fetch", flag.ExitOnError)
		source := fetchCmd.String("source", "", "optional markdown file to rebuild after fetch")
		touchAll := fetchCmd.Bool("touch-all", false, "update REV on all cards (apply immediately)")
		unBuckets := fetchCmd.Bool("un-contacts", false, "list UN_CONTACTS buckets instead of server contacts")
		fetchCmd.Parse(args[1:])
		client := newClient()
		if *unBuckets {
			printBuckets(getenv("UN_CONTACTS", "/home/pi/data/smbfs/dada/un-contacts"))
			return
		}
		infos := mustFetch(client)
		if *touchAll {
			ctx := context.Background()
			for _, cd := range infos {
				setRevNow(&cd.Card)
				if err := client.put(ctx, cd.Ref, cd.Card); err != nil {
					log.Printf("touch %s: %v", cd.Ref.Href, err)
				}
			}
			infos = mustFetch(client) // refetch after touch
		}
		printTable(infos)
		if *source != "" {
			writeTable(*source, infos)
			log.Printf("Wrote %s", *source)
		}
	case "add":
		addCmd := flag.NewFlagSet("add", flag.ExitOnError)
		name := addCmd.String("name", "", "name (required)")
		emails := addCmd.String("emails", "", "comma-separated emails")
		phones := addCmd.String("phones", "", "comma-separated phones")
		note := addCmd.String("note", "", "note")
		addCmd.Parse(args[1:])
		if *name == "" {
			log.Fatalf("name is required")
		}
		client := newClient()
		addEntry(client, desiredEntry{
			Name:   *name,
			Emails: splitCSV(*emails),
			Phones: splitCSV(*phones),
			Note:   *note,
		})
	case "update":
		upCmd := flag.NewFlagSet("update", flag.ExitOnError)
		name := upCmd.String("name", "", "existing name (required)")
		newName := upCmd.String("new-name", "", "new name")
		emails := upCmd.String("emails", "", "replace emails (comma-separated)")
		phones := upCmd.String("phones", "", "replace phones (comma-separated)")
		note := upCmd.String("note", "", "set note (empty to clear)")
		upCmd.Parse(args[1:])
		if *name == "" {
			log.Fatalf("name is required")
		}
		client := newClient()
		updateEntry(client, *name, *newName, splitCSV(*emails), splitCSV(*phones), note)
	case "delete", "remove", "rm":
		rmCmd := flag.NewFlagSet("delete", flag.ExitOnError)
		name := rmCmd.String("name", "", "name to delete (required)")
		backup := rmCmd.String("vcf", "", "optional backup path for the vcard")
		rmCmd.Parse(args[1:])
		if *name == "" {
			log.Fatalf("name is required")
		}
		client := newClient()
		deleteEntry(client, *name, *backup)
	case "move":
		mvCmd := flag.NewFlagSet("move", flag.ExitOnError)
		name := mvCmd.String("name", "", "name to move (required)")
		bucket := mvCmd.String("bucket", "", "target bucket under UN_CONTACTS (required)")
		newName := mvCmd.String("new-name", "", "optional new name before move")
		mvCmd.Parse(args[1:])
		if *name == "" || *bucket == "" {
			log.Fatalf("move: --name and --bucket are required")
		}
		moveEntry(newClient(), *name, *bucket, *newName)
	case "sync":
		syncCmd := flag.NewFlagSet("sync", flag.ExitOnError)
		source := syncCmd.String("source", "docs/examples/example-table.md", "markdown table to sync from")
		apply := syncCmd.Bool("apply", false, "apply changes (default dry-run)")
		touch := syncCmd.Bool("touch", false, "force-update REV on all cards")
		syncCmd.Parse(args[1:])
		runSync(*source, *apply, *touch)
	case "photos":
		photoCmd := flag.NewFlagSet("photos", flag.ExitOnError)
		apply := photoCmd.Bool("apply", false, "apply changes (default dry-run)")
		force := photoCmd.Bool("force", false, "replace existing photos")
		mapPath := photoCmd.String("map", getenv("PHOTO_MAP", "photo-map.json"), "photo map json")
		gravatar := photoCmd.Bool("gravatar", getenv("ENABLE_GRAVATAR", "0") != "0", "enable gravatar fallback")
		photoCmd.Parse(args[1:])
		applyPhotosCmd(*apply, *force, *mapPath, *gravatar)
	case "clean-buckets":
		cleanCmd := flag.NewFlagSet("clean-buckets", flag.ExitOnError)
		apply := cleanCmd.Bool("apply", false, "apply fixes (default dry-run)")
		cleanCmd.Parse(args[1:])
		cleanBuckets(*apply)
	case "refresh-uids":
		refreshCmd := flag.NewFlagSet("refresh-uids", flag.ExitOnError)
		apply := refreshCmd.Bool("apply", false, "apply changes (default dry-run)")
		refreshCmd.Parse(args[1:])
		refreshUIDs(*apply)
	case "photos-gravatar":
		log.Println("deprecated; use photos --gravatar")
	case "touch-all":
		client := newClient()
		infos := mustFetch(client)
		touchAllCards(client, infos)
	case "fix-names":
		fixCmd := flag.NewFlagSet("fix-names", flag.ExitOnError)
		apply := fixCmd.Bool("apply", false, "apply changes (default dry-run)")
		fixCmd.Parse(args[1:])
		fixNames(*apply)
	case "help", "-h", "--help":
		contactsUsage()
	default:
		contactsUsage()
	}
}

func contactsUsage() {
	fmt.Println("Usage: dav contacts <command> [options]")
	fmt.Println("Commands:")
	fmt.Println("  fetch          list contacts (fancy table) or buckets with --un-contacts; use --touch-all to bump REV")
	fmt.Println("  add            --name NAME [--emails e1,e2] [--phones p1,p2] [--note text]")
	fmt.Println("  update         --name NAME [--new-name NN] [--emails ...] [--phones ...] [--note text]")
	fmt.Println("  delete         --name NAME [--vcf /path/to/backup.vcf]")
	fmt.Println("  move           --name NAME --bucket psychology|corporate|... [--new-name NN]")
	fmt.Println("  sync           --source FILE [--apply] [--touch]  # reconcile to markdown table; extras go to UN_CONTACTS/neutral")
	fmt.Println("  photos         [--apply] [--force] [--map photo-map.json] [--gravatar bool]  # apply photo map/gravatar")
	fmt.Println("  clean-buckets  [--apply]  # normalize bucket phone ordering/format; warn on missing phones")
	fmt.Println("  refresh-uids   [--apply]  # recreate all server contacts with new UIDs/hrefs to force client refresh")
	fmt.Println("  fix-names      [--apply]  # set structured N to match FN for all server contacts")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  dav contacts fetch --touch-all")
	fmt.Println("  dav contacts fetch --un-contacts")
	fmt.Println("  dav contacts add --name \"Jane Doe\" --phones \"+1 4803957551,+91 9876543210\"")
	fmt.Println("  dav contacts move --name \"Vendor X\" --bucket corporate --new-name \"Vendor X (2019)\"")
	fmt.Println("  dav contacts delete --name \"Noise Lead\" --vcf \"$UN_CONTACTS/psychology/noise-lead.vcf\"")
	fmt.Println("  dav contacts photos --apply --gravatar")
	fmt.Println("  dav contacts sync --source docs/examples/example-table.md --apply --touch")
}

// touchAllCards bumps REV on all provided cards.
func touchAllCards(client *radClient, cards []cardData) {
	ctx := context.Background()
	for _, cd := range cards {
		setRevNow(&cd.Card)
		if err := client.put(ctx, cd.Ref, cd.Card); err != nil {
			log.Printf("touch %s: %v", cd.Ref.Href, err)
		}
	}
}

// client and HTTP

type radClient struct {
	base       string
	collection string
	user       string
	pass       string
}

func newClient() *radClient {
	// load .env if present in current directory
	loadDotEnv()
	baseURL := getenv("RADICALE_BASE_URL", "https://dav.gour.top/")
	collection := getenv("RADICALE_COLLECTION", "/dada/cf1c3fea-c8f4-02ab-efd3-bcc226e6b7cc/")
	user := os.Getenv("RADICALE_USER")
	pass := os.Getenv("RADICALE_PASS")
	if user == "" || pass == "" {
		log.Fatalf("RADICALE_USER/RADICALE_PASS required")
	}
	return &radClient{
		base:       strings.TrimRight(baseURL, "/") + "/",
		collection: strings.Trim(collection, "/"),
		user:       user,
		pass:       pass,
	}
}

func (c *radClient) collectionURL() string { return c.base + c.collection + "/" }

func (c *radClient) list(ctx context.Context) ([]cardRef, error) {
	body := `<?xml version="1.0"?>
<d:propfind xmlns:d="DAV:">
  <d:prop><d:getetag/><d:resourcetype/></d:prop>
</d:propfind>`
	req, _ := http.NewRequestWithContext(ctx, "PROPFIND", c.collectionURL(), strings.NewReader(body))
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "text/xml")
	req.SetBasicAuth(c.user, c.pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("propfind status %d: %s", resp.StatusCode, string(b))
	}
	var multistatus struct {
		Responses []struct {
			Href string `xml:"href"`
			Prop struct {
				ETag string `xml:"propstat>prop>getetag"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&multistatus); err != nil {
		return nil, err
	}
	refs := []cardRef{}
	for _, r := range multistatus.Responses {
		h := strings.TrimSpace(r.Href)
		if h == "" || strings.HasSuffix(h, "/") || !strings.HasSuffix(strings.ToLower(h), ".vcf") {
			continue
		}
		refs = append(refs, cardRef{Href: h, ETag: strings.Trim(r.Prop.ETag, `"`)})
	}
	return refs, nil
}

func (c *radClient) get(ctx context.Context, ref cardRef) (cardData, error) {
	url := ref.Href
	if !strings.HasPrefix(url, "http") {
		url = c.base + strings.TrimPrefix(ref.Href, "/")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.SetBasicAuth(c.user, c.pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return cardData{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return cardData{}, fmt.Errorf("get status %d: %s", resp.StatusCode, string(b))
	}
	dec := vcard.NewDecoder(resp.Body)
	card, err := dec.Decode()
	if err != nil {
		return cardData{}, err
	}
	return cardData{Ref: ref, Card: card}, nil
}

func (c *radClient) put(ctx context.Context, ref cardRef, card vcard.Card) error {
	url := ref.Href
	if !strings.HasPrefix(url, "http") {
		url = c.base + strings.TrimPrefix(ref.Href, "/")
	}
	body := serializeCard(card)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(body))
	req.SetBasicAuth(c.user, c.pass)
	if ref.ETag != "" {
		req.Header.Set("If-Match", ref.ETag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *radClient) delete(ctx context.Context, ref cardRef) error {
	url := ref.Href
	if !strings.HasPrefix(url, "http") {
		url = c.base + strings.TrimPrefix(ref.Href, "/")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	req.SetBasicAuth(c.user, c.pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// Commands

func mustFetch(client *radClient) []cardData {
	ctx := context.Background()
	refs, err := client.list(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	res := []cardData{}
	for _, ref := range refs {
		cd, err := client.get(ctx, ref)
		if err != nil {
			log.Printf("warn: get %s: %v", ref.Href, err)
			continue
		}
		res = append(res, cd)
	}
	return res
}

func printTable(cards []cardData) {
	type row struct {
		Name   string
		Emails string
		Phones string
	}
	rows := []row{}
	nameW, emailW, phoneW := len("Name"), len("Emails"), len("Phones")
	for _, c := range cards {
		fn := strings.TrimSpace(c.Card.Value(vcard.FieldFormattedName))
		em := strings.Join(getValues(c.Card, vcard.FieldEmail), ", ")
		ph := strings.Join(getValues(c.Card, vcard.FieldTelephone), ", ")
		rows = append(rows, row{fn, em, ph})
		if len(fn) > nameW {
			nameW = len(fn)
		}
		if len(em) > emailW {
			emailW = len(em)
		}
		if len(ph) > phoneW {
			phoneW = len(ph)
		}
	}
	fmt.Printf("%-*s  %-*s  %-*s\n", nameW, "Name", emailW, "Emails", phoneW, "Phones")
	fmt.Printf("%s  %s  %s\n", strings.Repeat("-", nameW), strings.Repeat("-", emailW), strings.Repeat("-", phoneW))
	for _, r := range rows {
		fmt.Printf("%-*s  %-*s  %-*s\n", nameW, r.Name, emailW, r.Emails, phoneW, r.Phones)
	}
}

func printBuckets(root string) {
	type row struct {
		Bucket string
		Name   string
		Emails string
		Phones string
	}
	rows := []row{}
	entries := listBucketEntries(root)
	bW, nW, eW, pW := len("Bucket"), len("Name"), len("Emails"), len("Phones")
	keys := []string{}
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, bucket := range keys {
		list := entries[bucket]
		sort.Slice(list, func(i, j int) bool {
			return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
		})
		for _, ent := range list {
			rows = append(rows, row{bucket, ent.Name, ent.Emails, ent.Phones})
			if len(bucket) > bW {
				bW = len(bucket)
			}
			if len(ent.Name) > nW {
				nW = len(ent.Name)
			}
			if len(ent.Emails) > eW {
				eW = len(ent.Emails)
			}
			if len(ent.Phones) > pW {
				pW = len(ent.Phones)
			}
		}
	}
	fmt.Printf("%-*s  %-*s  %-*s  %-*s\n", bW, "Bucket", nW, "Name", eW, "Emails", pW, "Phones")
	fmt.Printf("%s  %s  %s  %s\n", strings.Repeat("-", bW), strings.Repeat("-", nW), strings.Repeat("-", eW), strings.Repeat("-", pW))
	for _, r := range rows {
		fmt.Printf("%-*s  %-*s  %-*s  %-*s\n", bW, r.Bucket, nW, r.Name, eW, r.Emails, pW, r.Phones)
	}
}

func addEntry(client *radClient, d desiredEntry) {
	ctx := context.Background()
	card := vcard.Card{}
	card.SetValue(vcard.FieldVersion, "4.0")
	card.SetValue(vcard.FieldFormattedName, d.Name)
	card.SetValue(vcard.FieldName, d.Name)
	for _, em := range d.Emails {
		if em == "" {
			continue
		}
		card.Add(vcard.FieldEmail, &vcard.Field{Value: strings.ToLower(em)})
	}
	for _, n := range normalizeAndOrderPhones(d.Phones) {
		card.Add(vcard.FieldTelephone, &vcard.Field{
			Value:  n,
			Params: map[string][]string{vcard.ParamType: {"cell"}},
		})
	}
	if d.Note != "" {
		card.SetValue(vcard.FieldNote, d.Note)
	}
	ensureUID(&card)
	href := fmt.Sprintf("%s%s.vcf", client.collectionURL(), randomID())
	if err := client.put(ctx, cardRef{Href: href}, card); err != nil {
		log.Fatalf("add: %v", err)
	}
	log.Printf("added %s", d.Name)
}

func updateEntry(client *radClient, name, newName string, emails, phones []string, note *string) {
	ctx := context.Background()
	cards := mustFetch(client)
	target := findByName(cards, name)
	if target == nil {
		log.Fatalf("update: %s not found", name)
	}
	if newName != "" {
		target.Card.SetValue(vcard.FieldFormattedName, newName)
		target.Card.SetValue(vcard.FieldName, newName)
	}
	if emails != nil && len(emails) > 0 {
		clearProps(&target.Card, vcard.FieldEmail)
		for _, em := range emails {
			if em == "" {
				continue
			}
			target.Card.Add(vcard.FieldEmail, &vcard.Field{Value: strings.ToLower(em)})
		}
	}
	if phones != nil && len(phones) > 0 {
		clearProps(&target.Card, vcard.FieldTelephone)
		for _, n := range normalizeAndOrderPhones(phones) {
			target.Card.Add(vcard.FieldTelephone, &vcard.Field{
				Value:  n,
				Params: map[string][]string{vcard.ParamType: {"cell"}},
			})
		}
	}
	if note != nil {
		if *note == "" {
			clearProps(&target.Card, vcard.FieldNote)
		} else {
			target.Card.SetValue(vcard.FieldNote, *note)
		}
	}
	ensureUID(&target.Card)
	if err := client.put(ctx, target.Ref, target.Card); err != nil {
		log.Fatalf("update: %v", err)
	}
	log.Printf("updated %s", name)
}

func deleteEntry(client *radClient, name string, backupPath string) {
	ctx := context.Background()
	cards := mustFetch(client)
	target := findByName(cards, name)
	if target == nil {
		log.Fatalf("delete: %s not found", name)
	}
	// backup
	fname := backupPath
	if fname == "" {
		fname = safeFileName(name) + ".vcf"
		log.Printf("backup path not provided (--vcf). Saving to %s in current directory.", fname)
	}
	if err := os.WriteFile(fname, []byte(serializeCard(target.Card)), 0o644); err != nil {
		log.Fatalf("backup write failed: %v", err)
	}
	if err := client.delete(ctx, target.Ref); err != nil {
		log.Fatalf("delete: %v", err)
	}
	log.Printf("deleted %s (backup at %s)", name, fname)
}

func moveEntry(client *radClient, name string, bucket string, newName string) {
	ctx := context.Background()
	cards := mustFetch(client)
	target := findByName(cards, name)
	if target == nil {
		log.Fatalf("move: %s not found", name)
	}
	if newName != "" {
		target.Card.SetValue(vcard.FieldFormattedName, newName)
	}
	destDir := filepath.Join(getenv("UN_CONTACTS", "/home/pi/data/smbfs/dada/un-contacts"), bucket)
	_ = os.MkdirAll(destDir, 0o755)
	fname := filepath.Join(destDir, safeFileName(target.Card.Value(vcard.FieldFormattedName))+".vcf")
	if err := os.WriteFile(fname, []byte(serializeCard(target.Card)), 0o644); err != nil {
		log.Fatalf("move backup failed: %v", err)
	}
	if err := client.delete(ctx, target.Ref); err != nil {
		log.Fatalf("move delete failed: %v", err)
	}
	log.Printf("moved %s to %s", target.Card.Value(vcard.FieldFormattedName), fname)
}

// Sync workflow

func runSync(source string, apply bool, touch bool) {
	desired, err := parseDesired(source)
	if err != nil {
		log.Fatalf("parse desired: %v", err)
	}
	client := newClient()
	bucketRoot := getenv("UN_CONTACTS", "/home/pi/data/smbfs/dada/un-contacts")
	ctx := context.Background()
	refs, err := client.list(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	// fetch and dedupe by name
	allCards := []cardData{}
	for _, ref := range refs {
		card, err := client.get(ctx, ref)
		if err != nil {
			log.Printf("warn: get %s: %v", ref.Href, err)
			continue
		}
		allCards = append(allCards, card)
	}
	allCards = dedupeByName(ctx, client, allCards, apply)
	remote := map[string]cardData{}
	for _, cd := range allCards {
		fn := strings.TrimSpace(cd.Card.Value(vcard.FieldFormattedName))
		if touch {
			setRevNow(&cd.Card)
		}
		remote[norm(fn)] = cd
		if touch && apply {
			if err := client.put(ctx, cd.Ref, cd.Card); err != nil {
				log.Printf("touch put %s: %v", cd.Ref.Href, err)
			}
		}
	}
	desiredSet := map[string]bool{}
	for _, d := range desired {
		desiredSet[norm(d.Name)] = true
	}
	// remove extras not in desired
	for _, cd := range allCards {
		key := norm(cd.Card.Value(vcard.FieldFormattedName))
		if desiredSet[key] {
			continue
		}
		if apply {
			destDir := filepath.Join(bucketRoot, "neutral")
			_ = os.MkdirAll(destDir, 0o755)
			fname := filepath.Join(destDir, safeFileName(cd.Card.Value(vcard.FieldFormattedName))+".vcf")
			_ = os.WriteFile(fname, []byte(serializeCard(cd.Card)), 0o644)
			if err := client.delete(ctx, cd.Ref); err != nil {
				log.Printf("delete extra %s: %v", cd.Ref.Href, err)
			}
		} else {
			log.Printf("[dry-run] would remove extra %s", cd.Card.Value(vcard.FieldFormattedName))
		}
	}
	// load photo map & gravatar flag
	photoMap := loadPhotoMap(getenv("PHOTO_MAP", "photo-map.json"))
	enableGravatar := getenv("ENABLE_GRAVATAR", "0") != "0"

	// apply desired
	for _, d := range desired {
		key := norm(d.Name)
		if existing, ok := remote[key]; ok {
			updated := applyDesired(&existing.Card, d, photoMap, enableGravatar, false)
			if updated && apply {
				if err := client.put(ctx, existing.Ref, existing.Card); err != nil {
					log.Printf("put %s: %v", existing.Ref.Href, err)
				}
			}
		} else {
			card := vcard.Card{}
			card.SetValue(vcard.FieldVersion, "4.0")
			card.SetValue(vcard.FieldFormattedName, d.Name)
			for _, em := range d.Emails {
				if em == "" {
					continue
				}
				card.Add(vcard.FieldEmail, &vcard.Field{Value: strings.ToLower(em)})
			}
			for _, n := range normalizeAndOrderPhones(d.Phones) {
				card.Add(vcard.FieldTelephone, &vcard.Field{
					Value:  n,
					Params: map[string][]string{vcard.ParamType: {"cell"}},
				})
			}
			if d.Note != "" {
				card.SetValue(vcard.FieldNote, d.Note)
			}
			ensureUID(&card)
			ref := cardRef{Href: fmt.Sprintf("%s%s.vcf", client.collectionURL(), randomID())}
			if apply {
				if err := client.put(ctx, ref, card); err != nil {
					log.Printf("put new %s: %v", d.Name, err)
				}
			}
		}
	}
	// write verification table
	infos := mustFetch(client)
	writeTable("all-contacts-synced.md", infos)
	log.Printf("Wrote all-contacts-synced.md (%d rows)", len(infos))
}

// Helpers

func parseDesired(path string) ([]desiredEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var res []desiredEntry
	// simple markdown parser by pipes
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(buf), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|---") || strings.Contains(line, "Name | Emails") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 6 {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(parts[1], "✅"))
		emails := splitCSV(parts[2])
		phones := splitCSV(parts[3])
		note := strings.TrimSpace(parts[4])
		res = append(res, desiredEntry{Name: name, Emails: emails, Phones: phones, Note: note})
	}
	return res, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		val := strings.TrimSpace(p)
		if val != "" {
			out = append(out, val)
		}
	}
	return out
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func normalizePhone(num string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r == '+' {
			return r
		}
		return -1
	}, num)
	if cleaned == "" {
		return ""
	}
	digits := strings.TrimPrefix(cleaned, "+")
	if strings.HasPrefix(cleaned, "+0") && len(digits) == 11 {
		cleaned = "+91" + digits[1:]
		digits = strings.TrimPrefix(cleaned, "+")
	}
	if !strings.HasPrefix(cleaned, "+") {
		if strings.HasPrefix(digits, "0") && len(digits) == 11 {
			digits = digits[1:]
		}
		if len(digits) == 10 {
			cleaned = "+91" + digits
		} else {
			cleaned = "+" + digits
		}
	}
	digits = strings.TrimPrefix(cleaned, "+")
	if strings.HasPrefix(cleaned, "+91") && len(digits) == 12 {
		return fmt.Sprintf("+91 %s %s", digits[2:7], digits[7:])
	}
	if strings.HasPrefix(cleaned, "+1") && len(digits) == 11 {
		return fmt.Sprintf("+1 %s %s %s", digits[0:3], digits[3:6], digits[6:])
	}
	return cleaned
}

func normalizeAndOrderPhones(nums []string) []string {
	intl := []string{}
	india := []string{}
	seen := map[string]bool{}
	for _, ph := range nums {
		n := normalizePhone(ph)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if strings.HasPrefix(n, "+91") {
			india = append(india, n)
		} else {
			intl = append(intl, n)
		}
	}
	return append(intl, india...)
}

func normalizePhonesInCard(card *vcard.Card) {
	nums := []string{}
	for _, tel := range (*card)[vcard.FieldTelephone] {
		nums = append(nums, tel.Value)
	}
	ordered := normalizeAndOrderPhones(nums)
	clearProps(card, vcard.FieldTelephone)
	for _, num := range ordered {
		card.Add(vcard.FieldTelephone, &vcard.Field{
			Value:  num,
			Params: map[string][]string{vcard.ParamType: {"cell"}},
		})
	}
}

func getValues(c vcard.Card, field string) []string {
	vals := []string{}
	for _, f := range c[field] {
		if v := f.Value; v != "" {
			vals = append(vals, v)
		}
	}
	return vals
}

func clearProps(c *vcard.Card, field string) { delete(*c, field) }

func serializeCard(card vcard.Card) string {
	ensureUID(&card)
	setRevNow(&card)
	var b strings.Builder
	enc := vcard.NewEncoder(&b)
	_ = enc.Encode(card)
	return b.String()
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func safeFileName(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, s)
	return strings.Trim(s, "-")
}

func ensureUID(card *vcard.Card) {
	if card == nil {
		return
	}
	if val := card.Value(vcard.FieldUID); strings.TrimSpace(val) != "" {
		return
	}
	card.SetValue(vcard.FieldUID, fmt.Sprintf("uid-%s", randomID()))
}

// refreshUIDs recreates all server contacts with new UID/href to force clients to refetch.
func refreshUIDs(apply bool) {
	client := newClient()
	ctx := context.Background()
	cards := mustFetch(client)
	updated := 0
	for _, cd := range cards {
		newCard := cd.Card
		newCard.SetValue(vcard.FieldUID, fmt.Sprintf("uid-%s", randomID()))
		newCard.SetValue(vcard.FieldName, newCard.Value(vcard.FieldFormattedName))
		setRevNow(&newCard)
		newHref := fmt.Sprintf("%s%s.vcf", client.collectionURL(), randomID())
		if apply {
			if err := client.put(ctx, cardRef{Href: newHref}, newCard); err != nil {
				log.Printf("refresh put %s: %v", cd.Card.Value(vcard.FieldFormattedName), err)
				continue
			}
			if err := client.delete(ctx, cd.Ref); err != nil {
				log.Printf("refresh delete %s: %v", cd.Ref.Href, err)
			}
		} else {
			log.Printf("[dry-run] would recreate %s with new UID/href", cd.Card.Value(vcard.FieldFormattedName))
		}
		updated++
	}
	log.Printf("refresh-uids processed %d contact(s). apply=%v", updated, apply)
}

func applyPhoto(card *vcard.Card, name string, emails []string, photos map[string]string, enableGravatar bool, force bool) bool {
	if card == nil {
		return false
	}
	if !force && len((*card)[vcard.FieldPhoto]) > 0 {
		return false
	}
	if path, ok := photos[norm(name)]; ok {
		if data, err := os.ReadFile(path); err == nil {
			card.Add(vcard.FieldPhoto, &vcard.Field{
				Value:  base64.StdEncoding.EncodeToString(data),
				Params: map[string][]string{vcard.ParamValue: {"BINARY"}, vcard.ParamType: {"JPEG"}},
			})
			return true
		}
	}
	if enableGravatar && len(emails) > 0 {
		if b64, ok := fetchGravatar(emails[0]); ok {
			card.Add(vcard.FieldPhoto, &vcard.Field{
				Value:  b64,
				Params: map[string][]string{vcard.ParamValue: {"BINARY"}, vcard.ParamType: {"JPEG"}},
			})
			return true
		}
	}
	return false
}

func setRevNow(card *vcard.Card) {
	if card == nil {
		return
	}
	card.SetValue(vcard.FieldRevision, time.Now().UTC().Format("20060102T150405Z"))
}

func loadPhotoMap(path string) map[string]string {
	res := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return res
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return res
	}
	for k, v := range m {
		if v == "" {
			continue
		}
		res[norm(k)] = v
	}
	return res
}

func applyPhotosCmd(apply bool, force bool, mapPath string, gravatar bool) {
	client := newClient()
	ctx := context.Background()
	cards := mustFetch(client)
	photoMap := loadPhotoMap(mapPath)
	updated := 0
	for _, cd := range cards {
		fn := cd.Card.Value(vcard.FieldFormattedName)
		emails := getValues(cd.Card, vcard.FieldEmail)
		if applyPhoto(&cd.Card, fn, emails, photoMap, gravatar, force) {
			updated++
			if apply {
				if err := client.put(ctx, cd.Ref, cd.Card); err != nil {
					log.Printf("put %s: %v", cd.Ref.Href, err)
				}
			} else {
				log.Printf("[dry-run] would add photo to %s", fn)
			}
		}
	}
	log.Printf("Photos updated: %d (apply=%v)", updated, apply)
}

func fetchGravatar(email string) (string, bool) {
	hash := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
	url := fmt.Sprintf("https://www.gravatar.com/avatar/%x?d=404&s=256", hash)
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return "", false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(data), true
}

type bucketEntry struct {
	Name   string
	Emails string
	Phones string
	Path   string
}

func listBucketEntries(root string) map[string][]bucketEntry {
	res := map[string][]bucketEntry{}
	seen := map[string]string{} // norm name -> path kept
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".vcf" {
			return nil
		}
		bucket := filepath.Base(filepath.Dir(path))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		dec := vcard.NewDecoder(strings.NewReader(string(data)))
		for {
			card, err := dec.Decode()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			name := card.Value(vcard.FieldFormattedName)
			key := norm(name)
			if prev, ok := seen[key]; ok && prev != path {
				_ = os.Remove(path)
				break
			}
			seen[key] = path
			emails := strings.Join(getValues(card, vcard.FieldEmail), ", ")
			phones := strings.Join(getValues(card, vcard.FieldTelephone), ", ")
			res[bucket] = append(res[bucket], bucketEntry{Name: name, Emails: emails, Phones: phones, Path: path})
		}
		return nil
	})
	return res
}

func writeTable(path string, cards []cardData) {
	lines := []string{"# All contacts (Radicale) synced\n\n", "| Name | Emails | Phones | Note | Comments |\n", "|---|---|---|---|---|\n"}
	sort.Slice(cards, func(i, j int) bool {
		return strings.ToLower(cards[i].Card.Value(vcard.FieldFormattedName)) < strings.ToLower(cards[j].Card.Value(vcard.FieldFormattedName))
	})
	for _, c := range cards {
		name := c.Card.Value(vcard.FieldFormattedName)
		emails := strings.Join(getValues(c.Card, vcard.FieldEmail), ", ")
		phones := strings.Join(getValues(c.Card, vcard.FieldTelephone), ", ")
		note := ""
		if v := c.Card.Value(vcard.FieldNote); v != "" {
			note = v
		}
		lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s |  |\n", name, emails, phones, note))
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644)
}

func findByName(cards []cardData, name string) *cardData {
	key := norm(name)
	for i := range cards {
		fn := strings.TrimSpace(cards[i].Card.Value(vcard.FieldFormattedName))
		if norm(fn) == key {
			return &cards[i]
		}
	}
	// fallback exact match with trimming leading/trailing unicode artifacts
	for i := range cards {
		fn := strings.TrimSpace(cards[i].Card.Value(vcard.FieldFormattedName))
		fn = strings.Trim(fn, "\uFEFF\u200B")
		if norm(fn) == key {
			return &cards[i]
		}
	}
	return nil
}

func normalizeVCFPhones(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	dec := vcard.NewDecoder(strings.NewReader(string(data)))
	var cards []vcard.Card
	for {
		c, err := dec.Decode()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		cards = append(cards, c)
	}
	if len(cards) == 0 {
		return false
	}
	// gather all phones from all cards
	allNums := []string{}
	for _, c := range cards {
		for _, tel := range c[vcard.FieldTelephone] {
			allNums = append(allNums, tel.Value)
		}
	}
	primary := cards[0]
	primary.SetValue(vcard.FieldName, primary.Value(vcard.FieldFormattedName))
	// normalize combined set
	ordered := normalizeAndOrderPhones(allNums)
	clearProps(&primary, vcard.FieldTelephone)
	for _, num := range ordered {
		primary.Add(vcard.FieldTelephone, &vcard.Field{
			Value:  num,
			Params: map[string][]string{vcard.ParamType: {"cell"}},
		})
	}
	return os.WriteFile(path, []byte(serializeCard(primary)), 0o644) == nil
}

// cleanBuckets normalizes phone values/order in bucket VCFs; reports missing phones.
// It does not delete entries; apply=false is dry-run.
func cleanBuckets(apply bool) {
	root := getenv("UN_CONTACTS", "/home/pi/data/smbfs/dada/un-contacts")
	entries := listBucketEntries(root)
	fixed := 0
	for bucket, list := range entries {
		for _, ent := range list {
			if strings.TrimSpace(ent.Phones) == "" {
				log.Printf("[warn] %s missing phone: %s (%s)", bucket, ent.Name, ent.Path)
			}
			if !apply || ent.Path == "" {
				continue
			}
			if normalizeVCFPhones(ent.Path) {
				fixed++
			}
		}
	}
	log.Printf("clean-buckets: normalized %d file(s). apply=%v", fixed, apply)
}

func dedupeByName(ctx context.Context, client *radClient, cards []cardData, apply bool) []cardData {
	group := map[string][]cardData{}
	for _, c := range cards {
		key := norm(c.Card.Value(vcard.FieldFormattedName))
		group[key] = append(group[key], c)
	}
	kept := []cardData{}
	for _, list := range group {
		if len(list) == 0 {
			continue
		}
		// keep the first
		kept = append(kept, list[0])
		for _, extra := range list[1:] {
			if apply {
				if err := client.delete(ctx, extra.Ref); err != nil {
					log.Printf("delete duplicate %s: %v", extra.Ref.Href, err)
				}
			}
		}
	}
	return kept
}

// applyDesired mutates the card to match desired entry; returns true if changed.
func applyDesired(card *vcard.Card, d desiredEntry, photos map[string]string, enableGravatar bool, forcePhoto bool) bool {
	changed := false
	ensureUID(card)
	if card.Value(vcard.FieldFormattedName) != d.Name {
		card.SetValue(vcard.FieldFormattedName, d.Name)
		changed = true
	}
	// keep N aligned to FN
	if name := card.Value(vcard.FieldName); strings.TrimSpace(name) != d.Name {
		card.SetValue(vcard.FieldName, d.Name)
		changed = true
	}
	// emails
	clearProps(card, vcard.FieldEmail)
	for _, em := range d.Emails {
		if em == "" {
			continue
		}
		card.Add(vcard.FieldEmail, &vcard.Field{Value: strings.ToLower(em)})
	}
	// phones
	clearProps(card, vcard.FieldTelephone)
	for _, n := range normalizeAndOrderPhones(d.Phones) {
		card.Add(vcard.FieldTelephone, &vcard.Field{
			Value:  n,
			Params: map[string][]string{vcard.ParamType: {"cell"}},
		})
	}
	// note
	if d.Note != "" {
		card.SetValue(vcard.FieldNote, d.Note)
	}
	// photo if missing
	changed = applyPhoto(card, d.Name, d.Emails, photos, enableGravatar, forcePhoto) || changed
	return changed
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadDotEnv() {
	paths := []string{".env", filepath.Join(os.Getenv("HOME"), "git", "dav-manager", ".env")}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			parts := strings.SplitN(l, "=", 2)
			if len(parts) != 2 {
				continue
			}
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

// fixNames sets structured N to match FN for every server contact.
// This helps Android/WhatsApp pick up renamed contacts consistently.
func fixNames(apply bool) {
	client := newClient()
	ctx := context.Background()
	cards := mustFetch(client)
	updated := 0
	for _, cd := range cards {
		fn := strings.TrimSpace(cd.Card.Value(vcard.FieldFormattedName))
		if fn == "" {
			continue
		}
		n := strings.TrimSpace(cd.Card.Value(vcard.FieldName))
		if n == fn {
			continue
		}
		cd.Card.SetValue(vcard.FieldName, fn)
		setRevNow(&cd.Card)
		updated++
		if apply {
			if err := client.put(ctx, cd.Ref, cd.Card); err != nil {
				log.Printf("fix-names put %s: %v", cd.Ref.Href, err)
			}
		} else {
			log.Printf("[dry-run] would set N to FN for %s", fn)
		}
	}
	log.Printf("fix-names updated %d contact(s). apply=%v", updated, apply)
}
