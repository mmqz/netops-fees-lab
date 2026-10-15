// Command netopslab is the single NetOps Lab binary. Subcommands (all
// successful output is one JSON object on stdout; structured logs go to
// stderr; exit codes: 0 success / 1 failure / 2 usage):
//
//	netopslab serve    --addr :8080 --db ./data/netops.db --data-dir ./data
//	                   [--webhook-url U] [--detect-interval D] [--auth-token T]
//	netopslab analyze  --file path.xlsx --db path
//	netopslab import   --file path.xlsx --map map.json --db path
//	netopslab validate --db path [--batch id]
//	netopslab report   --db path [--batch id] --format html|json [--out path]
//	netopslab query    --db path resources|stats|relations …
//	netopslab backup   --db path --out snapshot.db
//	netopslab mcp      [--db path]        (also env NETOPS_DB)
//	netopslab testdata --out dir
package main

import (
	"os"

	"github.com/mmqz/netops-lab/internal/app"
)

func main() {
	os.Exit(app.CLI(os.Args[1:], os.Stdout, os.Stderr))
}
