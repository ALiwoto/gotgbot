package main

import (
	"fmt"
	"html/template"
	"strings"
)

// TODO: dont hardcode; obtain these from reply_markup fields.
var replyMarkupTypes = []string{
	"InlineKeyboardMarkup",
	"ReplyKeyboardMarkup",
	"ReplyKeyboardRemove",
	"ForceReply",
}

var (
	genericInterfaceTmpl    = template.Must(template.New("genericInterface").Parse(genericInterfaceMethod))
	inputMediaInterfaceTmpl = template.Must(template.New("inputMediaInterface").Parse(inputMediaInterfaceMethod))
	customMarshalTmpl       = template.Must(template.New("customMarshal").Parse(customMarshal))
)

func generateTypes(d APIDescription) error {
	file := strings.Builder{}
	file.WriteString(`
// THIS FILE IS AUTOGENERATED. DO NOT EDIT.
// Regen by running 'go generate' in the repo root.

package gen

import (
	"encoding/json"
	"fmt"
	"io"
)
`)

	for _, tgTypeName := range orderedTgTypes(d) {
		tgType := d.Types[tgTypeName]

		typeDef, err := generateTypeDef(d, tgType)
		if err != nil {
			return fmt.Errorf("failed to generate type definition of %s: %w", tgTypeName, err)
		}

		file.WriteString(typeDef)
	}

	// the reply_markup field is weird; this allows it to support multiple types.
	file.WriteString(generateGenericInterfaceType("ReplyMarkup", true))

	return writeGenToFile(file, "gen/gen_types.go")
}

func generateTypeDef(d APIDescription, tgType TypeDescription) (string, error) {
	typeDef := strings.Builder{}

	for _, d := range tgType.Description {
		typeDef.WriteString("\n// " + d)
	}

	typeDef.WriteString("\n// " + tgType.Href)

	if len(tgType.Fields) == 0 {
		switch tgType.Name {
		case tgTypeInputMedia:
			typeDef.WriteString(generateInputMediaInterfaceType(tgType.Name, tgType))

		case tgTypeCallbackGame,
			tgTypeInlineQueryResult,
			tgTypeInputFile,
			tgTypeInputMessageContent,
			tgTypePassportElementError:
			typeDef.WriteString(generateGenericInterfaceType(tgType.Name, len(tgType.Subtypes) != 0))

		default:
			return "", fmt.Errorf("unknown interface type %s - please make sure this doesnt require implementation", tgType.Name)
		}

		return typeDef.String(), nil
	}

	typeDef.WriteString("\ntype " + tgType.Name + " struct {")

	for _, f := range tgType.Fields {
		fieldType, err := f.getPreferredType()
		if err != nil {
			return "", fmt.Errorf("failed to get preferred type: %w", err)
		}

		if isSubtypeOf(tgType, tgTypeInlineQueryResult) {
			// we don't write the type field since it isnt something that should be customised. This is set in the custom marshaller.
			if f.Name == "type" {
				continue
			}
		} else if isSubtypeOf(tgType, tgTypeInputMedia) {
			// we don't write the type field since it isnt something that should be customised. This is set in the custom marshaller.
			if f.Name == "type" {
				continue
			}

			// We manually override the media field to have InputFile type on all inputmedia to allow reuse of fileuploads logic.
			if f.Name == "media" {
				fieldType = tgTypeInputFile
			}
		}

		if isTgType(d, fieldType) && !f.Required {
			fieldType = "*" + fieldType
		}

		typeDef.WriteString("\n// " + f.Description)
		typeDef.WriteString("\n" + snakeToTitle(f.Name) + " " + fieldType + " `json:\"" + f.Name + ",omitempty\"`")
	}

	typeDef.WriteString("\n}")

	for _, parentType := range tgType.SubtypeOf {
		switch parentType {
		case tgTypeInputMedia:
			// InputMedia items need a custom marshaller to handle the "type" field
			typeName := strings.TrimPrefix(tgType.Name, tgTypeInputMedia)

			err := customMarshalTmpl.Execute(&typeDef, customMarshalData{
				Type:     tgType.Name,
				TypeName: titleToSnake(typeName),
			})
			if err != nil {
				return "", fmt.Errorf("failed to generate custom marshal function for %s: %w", tgType.Name, err)
			}

			// We also need to setup the interface method
			err = inputMediaInterfaceTmpl.Execute(&typeDef, interfaceMethodData{
				Type:       tgType.Name,
				ParentType: parentType,
			})
			if err != nil {
				return "", fmt.Errorf("failed to generate %s interface methods for %s: %w", parentType, tgType.Name, err)
			}

		case tgTypeInlineQueryResult:
			// InlineQueryResult items need a custom marshaller to handle the "type" field
			typeName := strings.TrimPrefix(tgType.Name, tgTypeInlineQueryResult)
			typeName = strings.TrimPrefix(typeName, "Cached") // some of them are "Cached"

			err := customMarshalTmpl.Execute(&typeDef, customMarshalData{
				Type:     tgType.Name,
				TypeName: titleToSnake(typeName),
			})
			if err != nil {
				return "", fmt.Errorf("failed to generate custom marshal function for %s: %w", tgType.Name, err)
			}

			err = genericInterfaceTmpl.Execute(&typeDef, interfaceMethodData{
				Type:       tgType.Name,
				ParentType: parentType,
			})
			if err != nil {
				return "", fmt.Errorf("failed to generate %s interface methods for %s: %w", parentType, tgType.Name, err)
			}

		case tgTypeInputMessageContent, tgTypePassportElementError:
			err := genericInterfaceTmpl.Execute(&typeDef, interfaceMethodData{
				Type:       tgType.Name,
				ParentType: parentType,
			})
			if err != nil {
				return "", fmt.Errorf("failed to generate %s interface methods for %s: %w", parentType, tgType.Name, err)
			}

		default:
			return "", fmt.Errorf("unable to handle parent type %s while generating for type %s\n", parentType, tgType.Name)
		}
	}

	for _, t := range replyMarkupTypes {
		if tgType.Name == t {
			err := genericInterfaceTmpl.Execute(&typeDef, interfaceMethodData{
				Type:       tgType.Name,
				ParentType: "ReplyMarkup",
			})
			if err != nil {
				return "", fmt.Errorf("failed to generate replymarkup interface methods for %s: %w", tgType.Name, err)
			}

			break
		}
	}

	return typeDef.String(), nil
}

func generateInputMediaInterfaceType(name string, tgType TypeDescription) string {
	if len(tgType.Subtypes) != 0 {
		return fmt.Sprintf(`
type %s interface{
	%sParams(string, map[string]NamedReader) ([]byte, error)
}`, name, name)
	}

	return "\ntype " + name + " interface{}"
}

func generateGenericInterfaceType(name string, hasSubtypes bool) string {
	if !hasSubtypes {
		return "\ntype " + name + " interface{}"
	}

	return fmt.Sprintf(`
type %s interface{
	%s() ([]byte, error)
}`, name, name)
}

func isSubtypeOf(tgType TypeDescription, parentType string) bool {
	for _, pt := range tgType.SubtypeOf {
		if parentType == pt {
			return true
		}
	}

	return false
}

type customMarshalData struct {
	Type     string
	TypeName string
}

const customMarshal = `
func (v {{.Type}}) MarshalJSON() ([]byte, error) {
	type alias {{.Type}}
	a := struct{
		Type string
		alias
	}{
		Type: "{{.TypeName}}",
		alias: (alias)(v),
	}
	return json.Marshal(a)
}
`

type interfaceMethodData struct {
	Type       string
	ParentType string
}

const inputMediaInterfaceMethod = `
func (v {{.Type}}) {{.ParentType}}Params(mediaName string, data map[string]NamedReader) ([]byte, error) {
	if v.Media != nil {
		switch m := v.Media.(type) {
		case string:
			// ok, noop

		case NamedReader:
			v.Media = "attach://" + mediaName
			data[mediaName] = m

		case io.Reader:
			v.Media = "attach://" + mediaName
			data[mediaName] = NamedFile{Reader: m}

		default:
			return nil, fmt.Errorf("unknown type for InputMedia: %T", v.Media)
		}
	}
	
	return json.Marshal(v)
}
`

const genericInterfaceMethod = `
func (v {{.Type}}) {{.ParentType}}() ([]byte, error) {
	return json.Marshal(v)
}
`
