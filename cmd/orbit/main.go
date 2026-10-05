// Command orbit manages cloud certificates: it matches certificates from the
// Tencent Cloud SSL service against CDN and EdgeOne domains, applies the best
// one, and removes certificates no cloud resource uses any more.
package main

import (
	"os"

	"github.com/Crequency/orbit/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
