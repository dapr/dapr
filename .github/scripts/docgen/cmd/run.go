package cmd

import (
	"fmt"
	"github.com/dapr/dapr/.github/scripts/docgen/pkg/daprd"
	"github.com/dapr/dapr/.github/scripts/docgen/pkg/markdown"
	"log"
)

func Run() {
	flags, err := daprd.ParseFlags("../../../cmd/daprd/options/options.go")
	if err != nil {
		log.Fatalf("error parsing flags: %s", err)
	}
	lines := markdown.GenerateDaprdTable(*flags)

	for _, l := range lines {
		fmt.Print(l)
	}
}
