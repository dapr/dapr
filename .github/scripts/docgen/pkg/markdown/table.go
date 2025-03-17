package markdown

import (
	"fmt"
	"github.com/dapr/dapr/.github/scripts/docgen/pkg/daprd"
	"github.com/dapr/dapr/.github/scripts/docgen/pkg/injector"
	"strings"
)

type row struct {
	Daprd              string
	Annotation         string
	Deprecated         bool
	DeprecationMessage string
	DefaultValue       string
	Description        string
}

func existsInRows(rows []row, annotation injector.AnnotationInfo) (index int) {
	annotationStripped := strings.ReplaceAll(annotation.Name, "dapr.io/", "")
	for i, r := range rows {
		if r.Daprd == annotationStripped {
			return i
		}
		if r.Daprd == annotation.DaprdEquivalent && annotation.DaprdEquivalent != "" {
			return i
		}
	}

	return -1
}

func CombineFlagsAndAnnotations(flags *map[string]daprd.FlagInfo, annotations []injector.AnnotationInfo) []row {

	var rows []row

	for k, v := range *flags {
		rows = append(rows, row{
			Daprd:        k,
			Annotation:   "",
			Deprecated:   v.Deprecated,
			DefaultValue: v.DefaultValue,
			Description:  v.Description,
		})
	}

	for _, a := range annotations {
		// if the annotation is already in the rows append the annotation,
		//description, deprecation and default value if empty

		index := existsInRows(rows, a)
		if index == -1 {
			rows = append(rows, row{
				Daprd:        "",
				Annotation:   a.Name,
				Deprecated:   a.Deprecated,
				DefaultValue: a.DefaultVal,
				Description:  a.Description,
			})
		} else {
			rows[index].Annotation = a.Name
			if rows[index].Deprecated == false {
				rows[index].Deprecated = a.Deprecated
			}
			if rows[index].DefaultValue == "" {
				rows[index].DefaultValue = a.DefaultVal
			}
			if rows[index].Description == "" {
				rows[index].Description = a.Description
			}

		}
	}

	return rows
}

func GenerateAllTable(rows []row) []string {
	var lines []string
	headers := "| daprd | Kubernetes Annotation | Default Value | Description |\n"
	lines = append(lines, headers)
	columnSizes := "| ----- | ---------- | ------------- | ----------- |\n"
	lines = append(lines, columnSizes)

	for _, r := range rows {

		var daprd string
		if daprd = r.Daprd; daprd == "" {
			daprd = "-"
		}

		var annotation string
		if annotation = r.Annotation; annotation == "" {
			annotation = "-"
		}

		var defaultValue string
		if defaultValue = r.DefaultValue; defaultValue == "" {
			defaultValue = "-"
		}

		var description string
		if description = r.Description; description == "" {
			description = "*No description available*"
		}

		if r.Deprecated {
			description = fmt.Sprintf("**DEPRECATED** %s <br> %s", r.DeprecationMessage, description)
		}

		var line string
		switch r.Deprecated {
		case true:
			line = fmt.Sprintf("| %s | %s | %s | **DEPRECATED** %s <br> %s |\n", daprd, annotation, defaultValue,
				description)
		case false:
			line = fmt.Sprintf("| %s | %s | %s | %s |\n", daprd, annotation, defaultValue, description)
		}

		lines = append(lines, line)
	}

	return lines
}

func GenerateDaprdTable(flags map[string]daprd.FlagInfo) []string {
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
