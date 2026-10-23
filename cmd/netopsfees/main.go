// Command netopsfees is the single NetOps Lab binary. Subcommands (all
// successful output is one JSON object on stdout; structured logs go to
// stderr; exit codes: 0 success / 1 failure / 2 usage):
//
//	netopsfees serve    --addr :8080 --db ./data/netops.db --data-dir ./data
//	                   [--webhook-url U] [--detect-interval D] [--auth-token T]
//	netopsfees analyze  --file path.xlsx --db path
//	netopsfees import   --file path.xlsx --map map.json --db path
//	netopsfees validate --db path [--batch id]
//	netopsfees report   --db path [--batch id] --format html|json [--out path]
//	netopsfees query    --db path resources|stats|relations …
//	netopsfees backup   --db path --out snapshot.db
//	netopsfees mcp      [--db path]        (also env NETOPS_DB)
//	netopsfees testdata --out dir
package main

import (
	"os"

	"github.com/mmqz/netops-fees-lab/internal/app"
)

func main() {
	os.Exit(app.CLI(os.Args[1:], os.Stdout, os.Stderr))
}
