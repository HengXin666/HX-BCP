package api

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/HengXin666/hx-pxy/internal/listener"
	"github.com/HengXin666/hx-pxy/internal/residential"
)

// The OpenAPI documents under docs/openapi are a contract, so they are checked
// against the Go types that render the responses, recursively: a field added,
// renamed or dropped on either side at any depth fails here. The two documents
// also stay disjoint: subscription services and residential channels are
// separate resources and must never share an operation.

type openAPIDocument struct {
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

func loadOpenAPI(t *testing.T, name string) openAPIDocument {
	t.Helper()
	raw, err := os.ReadFile("../../docs/openapi/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var document openAPIDocument
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return document
}

// objectSchema follows $ref and picks the object branch of a oneOf.
func (d openAPIDocument) objectSchema(schema map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		return d.objectSchema(d.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")])
	}
	if variants, ok := schema["oneOf"].([]any); ok {
		for _, variant := range variants {
			if candidate, ok := variant.(map[string]any); ok && candidate["type"] == "object" {
				return d.objectSchema(candidate)
			}
		}
	}
	return schema
}

func (d openAPIDocument) check(t *testing.T, path string, schema map[string]any, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		if typ.Kind() == reflect.Slice {
			items, _ := d.objectSchema(schema)["items"].(map[string]any)
			schema = items
		}
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{}) {
		return
	}
	schema = d.objectSchema(schema)
	properties, _ := schema["properties"].(map[string]any)
	documented := make([]string, 0, len(properties))
	for name := range properties {
		documented = append(documented, name)
	}
	sort.Strings(documented)
	rendered := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		rendered = append(rendered, name)
		if child, ok := properties[name].(map[string]any); ok {
			d.check(t, path+"."+name, child, field.Type)
		}
	}
	sort.Strings(rendered)
	if !reflect.DeepEqual(documented, rendered) {
		t.Errorf("%s properties = %v, Go type renders %v", path, documented, rendered)
	}
}

func TestOpenAPIDocumentsMatchResponseTypes(t *testing.T) {
	consumer := loadOpenAPI(t, "subscription-consumer.openapi.yaml")
	control := loadOpenAPI(t, "residential-control.openapi.yaml")
	cases := []struct {
		document openAPIDocument
		schema   string
		value    any
	}{
		{consumer, "ServiceNodes", listener.ConsumerPayload{}},
		{control, "ChannelNodes", residential.ControlNodeList{}},
		{control, "ChannelNode", residential.ControlNode{}},
	}
	for _, item := range cases {
		schema, ok := item.document.Components.Schemas[item.schema]
		if !ok {
			t.Fatalf("schema %s missing", item.schema)
		}
		item.document.check(t, item.schema, schema, reflect.TypeOf(item.value))
	}
}

func TestOpenAPIKeepsSubscriptionAndResidentialSurfacesApart(t *testing.T) {
	consumer := loadOpenAPI(t, "subscription-consumer.openapi.yaml")
	control := loadOpenAPI(t, "residential-control.openapi.yaml")
	for path := range consumer.Paths {
		if strings.HasPrefix(path, "/ctl/") || strings.HasPrefix(path, "/rot/") {
			t.Errorf("subscription document exposes residential path %s", path)
		}
	}
	for path := range control.Paths {
		if !strings.HasPrefix(path, "/ctl/") {
			t.Errorf("residential document exposes non-control path %s", path)
		}
	}
	if len(consumer.Paths) == 0 || len(control.Paths) == 0 {
		t.Fatal("documents must not be empty")
	}
}

func TestOpenAPISubscriptionMediaTypesMatchRenderer(t *testing.T) {
	document := loadOpenAPI(t, "subscription-consumer.openapi.yaml")
	get, _ := document.Paths["/sub/{shareToken}"]["get"].(map[string]any)
	responses, _ := get["responses"].(map[string]any)
	ok, _ := responses["200"].(map[string]any)
	content, _ := ok["content"].(map[string]any)
	export := listener.ShareExport{Name: "svc", Body: "http://u:p@example.com:1"}
	for _, format := range []string{"", "v2rayn", "uri", "clash", "sing-box"} {
		_, _, contentType, err := export.Render(format)
		if err != nil {
			t.Fatalf("render %q: %v", format, err)
		}
		mediaType := strings.TrimSpace(strings.Split(contentType, ";")[0])
		if _, documented := content[mediaType]; !documented {
			t.Errorf("format %q renders %s, which /sub 200 does not document", format, mediaType)
		}
	}
}
