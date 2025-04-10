package injector

import (
	"fmt"
	"github.com/dapr/dapr/pkg/injector/patcher"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

// AnnotationInfo represents documentation for a single annotation
type AnnotationInfo struct {
	Name            string
	Type            string
	DefaultVal      string
	Description     string
	DaprdEquivalent string
	Deprecated      bool
}

// GetSidecarAnnotations extracts annotation information from the SidecarConfig struct
func GetSidecarAnnotations() []AnnotationInfo {
	config := reflect.TypeOf(patcher.SidecarConfig{})

	fieldDescriptions := extractFieldDescriptions(patcher.SidecarConfig{})

	annotations := []AnnotationInfo{}

	for i := 0; i < config.NumField(); i++ {
		field := config.Field(i)

		annotationName := field.Tag.Get("annotation")
		if annotationName == "" {
			continue
		}

		defaultValue := field.Tag.Get("default")
		daprdEquivalent := field.Tag.Get("daprdEquivalentFlag")
		deprecated := field.Tag.Get("deprecated") == "true"

		fieldType := determineTypeString(field.Type)

		description := fieldDescriptions[field.Name]

		info := AnnotationInfo{
			Name:            annotationName,
			Type:            fieldType,
			DefaultVal:      defaultValue,
			Description:     description,
			DaprdEquivalent: daprdEquivalent,
			Deprecated:      deprecated,
		}

		annotations = append(annotations, info)
	}

	sort.Slice(annotations, func(i, j int) bool {
		return annotations[i].Name < annotations[j].Name
	})

	return annotations
}

// extractFieldDescriptions parses the source file of a struct to extract field descriptions from comments
func extractFieldDescriptions(structInstance interface{}) map[string]string {
	descriptions := make(map[string]string)

	_, filePath, _, _ := runtime.Caller(0)
	if structVal := reflect.ValueOf(structInstance); structVal.Kind() == reflect.Struct {
		pkgPath := reflect.TypeOf(structInstance).PkgPath()
		dir := filepath.Dir(filePath)
		for !strings.HasSuffix(filepath.Base(dir), "pkg") && dir != "/" {
			dir = filepath.Dir(dir)
		}

		pkgDir := filepath.Join(dir, strings.Replace(pkgPath, "github.com/dapr/dapr/pkg/", "../../../../pkg/", 1))

		fset := token.NewFileSet()

		pkgs, err := parser.ParseDir(fset, pkgDir, nil, parser.ParseComments)
		if err != nil {
			fmt.Printf("Error parsing directory %s: %v\n", pkgDir, err)
			return descriptions
		}

		structName := reflect.TypeOf(structInstance).Name()
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				ast.Inspect(file, func(n ast.Node) bool {
					typeSpec, ok := n.(*ast.TypeSpec)
					if !ok || typeSpec.Name.Name != structName {
						return true
					}

					structType, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						return false
					}

					for _, field := range structType.Fields.List {
						if len(field.Names) == 0 {
							continue
						}

						fieldName := field.Names[0].Name

						if field.Comment != nil && len(field.Comment.List) > 0 {
							comment := field.Comment.Text()
							if idx := strings.Index(comment, "Description:"); idx >= 0 {
								desc := strings.TrimSpace(comment[idx+len("Description:"):])
								descriptions[fieldName] = desc
							}
						}

						if field.End().IsValid() {
							for _, cmt := range file.Comments {
								if cmt.Pos() >= field.End() && cmt.Pos() <= field.End()+100 {
									comment := cmt.Text()
									if idx := strings.Index(comment, "Description:"); idx >= 0 {
										desc := strings.TrimSpace(comment[idx+len("Description:"):])
										descriptions[fieldName] = desc
									}
									break
								}
							}
						}
					}

					return false
				})
			}
		}
	}

	return descriptions
}

// determineTypeString returns a human-readable type description
func determineTypeString(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "unsigned integer"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Ptr:
		return fmt.Sprintf("optional %s", determineTypeString(t.Elem()))
	default:
		return t.String()
	}
}

// PrintAnnotationsMarkdown generates markdown documentation for the annotations
func PrintAnnotationsMarkdown() string {
	annotations := GetSidecarAnnotations()

	var sb strings.Builder
	sb.WriteString("# Dapr Sidecar Annotations\n\n")
	sb.WriteString("| Annotation | Type | Default | Description | daprd Equivalent | Deprecated |\n")
	sb.WriteString("|------------|------|---------|-------------|-----------------|------------|\n")

	for _, anno := range annotations {
		defaultVal := anno.DefaultVal
		if defaultVal == "" {
			defaultVal = "-"
		}

		description := anno.Description
		if description == "" {
			description = "*No description available*"
		}

		daprdEquivalent := anno.DaprdEquivalent
		if daprdEquivalent == "" {
			daprdEquivalent = "-"
		} else {
			daprdEquivalent = fmt.Sprintf("`--%s`", daprdEquivalent)
		}

		deprecated := "-"
		if anno.Deprecated {
			deprecated = "✓"
		}

		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s |\n",
			anno.Name, anno.Type, defaultVal, description, daprdEquivalent, deprecated))
	}

	return sb.String()
}
