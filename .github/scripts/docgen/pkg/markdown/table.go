package markdown

import (
	"fmt"
	"github.com/dapr/dapr/.github/scripts/docgen/pkg/daprd"
)

func GenerateDaprdTable(flags map[string]daprd.Flag) []string {
	var lines []string
	headers := "| daprd | Default Value | Description |\n"
	lines = append(lines, headers)
	columnSizes := "| ----- | ------- | ------------ |\n"
	lines = append(lines, columnSizes)

	for k, v := range flags {
		var line string
		switch v.Deprecated {
		case true:
			line = fmt.Sprintf("| --%s | %s | **DEPRECATED** %s <br> %s |\n", k, v.DefaultValue, v.DeprecationMessage,
				v.Description)
		case false:
			line = fmt.Sprintf("| --%s | %s | %s |\n", k, v.DefaultValue, v.Description)
		}

		lines = append(lines, line)
	}

	return lines
}
