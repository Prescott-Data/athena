// Command ltm-purge-unscoped deletes LTM graph nodes and edges written before the
// graph was scoped by tenant and user. Those entries are never returned to any
// tenant, but they still hold knowledge extracted from tenants' conversations, so
// deployments upgraded from such a release should remove them.
//
// Usage: ARANGODB_URL=... ARANGODB_PASSWORD=... go run ./cmd/ltm-purge-unscoped [-dry-run]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/arangodb/go-driver"
	arangohttp "github.com/arangodb/go-driver/http"
)

var collections = []string{"MemoryEdges", "Identities", "Concepts", "Tools", "Projects"}

func main() {
	dryRun := flag.Bool("dry-run", false, "count the unscoped entries without deleting them")
	flag.Parse()

	url := os.Getenv("ARANGODB_URL")
	if url == "" {
		log.Fatal("ARANGODB_URL must be set")
	}
	user := os.Getenv("ARANGODB_USER")
	if user == "" {
		user = "root"
	}
	pass := os.Getenv("ARANGO_ROOT_PASSWORD")
	if pass == "" {
		pass = os.Getenv("ARANGODB_PASSWORD")
	}
	dbName := os.Getenv("ARANGODB_DATABASE")
	if dbName == "" {
		dbName = "athena_ltm"
	}

	ctx := context.Background()
	conn, err := arangohttp.NewConnection(arangohttp.ConnectionConfig{Endpoints: []string{url}})
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	client, err := driver.NewClient(driver.ClientConfig{Connection: conn, Authentication: driver.BasicAuthentication(user, pass)})
	if err != nil {
		log.Fatalf("client: %v", err)
	}
	db, err := client.Database(ctx, dbName)
	if err != nil {
		log.Fatalf("open %s: %v", dbName, err)
	}

	total := 0
	for _, col := range collections {
		action := "REMOVE d IN " + col
		if *dryRun {
			action = ""
		}
		// Edges go first, so no edge is left pointing at a removed node.
		query := fmt.Sprintf(`FOR d IN %s FILTER d.tenant_id == null %s COLLECT WITH COUNT INTO n RETURN n`, col, action)
		cursor, err := db.Query(ctx, query, nil)
		if err != nil {
			log.Fatalf("%s: %v", col, err)
		}
		var n int
		if _, err := cursor.ReadDocument(ctx, &n); err != nil && !driver.IsNoMoreDocuments(err) {
			log.Fatalf("%s: %v", col, err)
		}
		cursor.Close()
		fmt.Printf("%-12s %d unscoped\n", col, n)
		total += n
	}
	if *dryRun {
		fmt.Printf("%d unscoped entries (dry run, nothing deleted)\n", total)
	} else {
		fmt.Printf("deleted %d unscoped entries\n", total)
	}
}
