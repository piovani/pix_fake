package contract_test

// A small validator of XML documents against the schemas of an OpenAPI file, enough for the DICT specification:
// objects, arrays (wrapped in an element), strings with enum/pattern/length/format, numbers, booleans, allOf, oneOf
// and the xml name and attribute annotations. It exists to check what the mock answers against what the Bacen file says,
// without any dependency.

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// --- The specification ---

type spec struct{ doc map[string]any }

func loadSpec(t *testing.T) *spec {
	t.Helper()
	raw, err := os.ReadFile("../../docs/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return &spec{doc: doc}
}

// resolve follows $ref (only "#/..." pointers) until it reaches the object.
func (s *spec) resolve(v any) map[string]any {
	m, _ := v.(map[string]any)
	for m != nil {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}
		cur := any(s.doc)
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cur = cur.(map[string]any)[part]
		}
		m, _ = cur.(map[string]any)
	}
	return nil
}

// refName is the last segment of a $ref, which names the root element of a message.
func refName(v any) string {
	m, _ := v.(map[string]any)
	ref, _ := m["$ref"].(string)
	return ref[strings.LastIndex(ref, "/")+1:]
}

// operation returns the operation object of a method and path.
func (s *spec) operation(method, path string) map[string]any {
	paths, _ := s.doc["paths"].(map[string]any)
	item, _ := paths[path].(map[string]any)
	op, _ := item[strings.ToLower(method)].(map[string]any)
	return op
}

// responseSchema returns the schema, as written (possibly a $ref), of a response, and the media type found.
func (s *spec) responseSchema(op map[string]any, status int) (schema any, mediaType string, ok bool) {
	responses, _ := op["responses"].(map[string]any)
	r := s.resolve(responses[strconv.Itoa(status)])
	if r == nil {
		return nil, "", false
	}
	content, _ := r["content"].(map[string]any)
	for mt, v := range content {
		if sch, _ := v.(map[string]any)["schema"]; sch != nil {
			return sch, mt, true
		}
	}
	return nil, "", true
}

// requestSchema returns the schema of the request body and its example, when it has one.
func (s *spec) requestBody(op map[string]any) (schema any, example string) {
	rb := s.resolve(op["requestBody"])
	content, _ := rb["content"].(map[string]any)
	app, _ := content["application/xml"].(map[string]any)
	if app == nil {
		return nil, ""
	}
	if ex, ok := app["examples"].(map[string]any); ok {
		for _, e := range ex {
			example, _ = e.(map[string]any)["value"].(string)
			break
		}
	}
	return app["schema"], example
}

// --- Flattened schemas ---

type sch struct {
	typ       string
	props     map[string]any
	required  map[string]bool
	enum      []any
	pattern   string
	maxLength int
	minLength int
	format    string
	xmlName   string
	xmlAttr   bool
	oneOf     []any
	items     any
}

func (s *spec) flatten(raw any) sch {
	m := s.resolve(raw)
	out := sch{props: map[string]any{}, required: map[string]bool{}, maxLength: -1, minLength: -1}
	if m == nil {
		return out
	}
	if parts, ok := m["allOf"].([]any); ok {
		for _, p := range parts {
			out.merge(s.flatten(p))
		}
	}
	own := sch{props: map[string]any{}, required: map[string]bool{}, maxLength: -1, minLength: -1}
	own.typ, _ = m["type"].(string)
	if props, ok := m["properties"].(map[string]any); ok {
		own.props = props
	}
	if req, ok := m["required"].([]any); ok {
		for _, r := range req {
			own.required[r.(string)] = true
		}
	}
	own.enum, _ = m["enum"].([]any)
	own.pattern, _ = m["pattern"].(string)
	if v, ok := m["maxLength"].(float64); ok {
		own.maxLength = int(v)
	}
	if v, ok := m["minLength"].(float64); ok {
		own.minLength = int(v)
	}
	own.format, _ = m["format"].(string)
	if x, ok := m["xml"].(map[string]any); ok {
		own.xmlName, _ = x["name"].(string)
		own.xmlAttr, _ = x["attribute"].(bool)
	}
	own.oneOf, _ = m["oneOf"].([]any)
	own.items = m["items"]
	out.merge(own)
	return out
}

// merge overlays o on s: a later part of an allOf wins, properties and required are added.
func (s *sch) merge(o sch) {
	if o.typ != "" {
		s.typ = o.typ
	}
	for k, v := range o.props {
		s.props[k] = v
	}
	for k := range o.required {
		s.required[k] = true
	}
	if o.enum != nil {
		s.enum = o.enum
	}
	if o.pattern != "" {
		s.pattern = o.pattern
	}
	if o.maxLength >= 0 {
		s.maxLength = o.maxLength
	}
	if o.minLength >= 0 {
		s.minLength = o.minLength
	}
	if o.format != "" {
		s.format = o.format
	}
	if o.xmlName != "" {
		s.xmlName = o.xmlName
	}
	if o.xmlAttr {
		s.xmlAttr = true
	}
	if o.oneOf != nil {
		s.oneOf = o.oneOf
	}
	if o.items != nil {
		s.items = o.items
	}
}

// --- XML tree ---

type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

func parseXML(body []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				n.attrs[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	return root, nil
}

// --- Validation ---

// validate checks the XML document against the schema (as written in the spec, so it may be a $ref) and returns
// every difference it finds. rootName is the name the root element must have.
func (s *spec) validate(body []byte, schema any, rootName string) []string {
	root, err := parseXML(body)
	if err != nil {
		return []string{"not XML: " + err.Error()}
	}
	var errs []string
	fl := s.flatten(schema)
	want := rootName
	if fl.xmlName != "" {
		want = fl.xmlName
	}
	if root.name != want {
		errs = append(errs, fmt.Sprintf("root element is <%s>, want <%s>", root.name, want))
	}
	s.node(root, fl, "/"+root.name, &errs)
	return errs
}

func (s *spec) node(n *node, sc sch, path string, errs *[]string) {
	add := func(format string, args ...any) { *errs = append(*errs, path+": "+fmt.Sprintf(format, args...)) }

	if len(sc.oneOf) > 0 {
		base := sc
		base.oneOf = nil
		var first []string
		for i, opt := range sc.oneOf {
			merged := base
			merged.props = map[string]any{}
			for k, v := range base.props {
				merged.props[k] = v
			}
			merged.required = map[string]bool{}
			for k := range base.required {
				merged.required[k] = true
			}
			merged.merge(s.flatten(opt))
			var sub []string
			s.node(n, merged, path, &sub)
			if len(sub) == 0 {
				return
			}
			if i == 0 {
				first = sub
			}
		}
		add("matches none of the %d alternatives (first one: %s)", len(sc.oneOf), strings.Join(first, "; "))
		return
	}

	// Attributes.
	for k, raw := range sc.props {
		ps := s.flatten(raw)
		if !ps.xmlAttr {
			continue
		}
		name := orStr(ps.xmlName, k)
		v, present := n.attrs[name]
		if !present {
			if sc.required[k] {
				add("missing attribute %s", name)
			}
			continue
		}
		s.primitive(v, ps, path+"/@"+name, errs)
	}

	// Elements.
	type prop struct {
		key string
		sc  sch
	}
	byName := map[string]prop{}
	for k, raw := range sc.props {
		ps := s.flatten(raw)
		if ps.xmlAttr {
			continue
		}
		byName[orStr(ps.xmlName, k)] = prop{k, ps}
	}
	if len(byName) == 0 && sc.typ != "object" {
		s.primitive(strings.TrimSpace(n.text), sc, path, errs)
		return
	}
	seen := map[string]int{}
	for _, kid := range n.kids {
		p, ok := byName[kid.name]
		if !ok {
			// an element the schema requires without declaring it (Total of ListFraudMarkersResponse) is expected
			if kid.name != "Signature" && !sc.required[kid.name] {
				add("unexpected element <%s>", kid.name)
			}
			continue
		}
		seen[kid.name]++
		if seen[kid.name] > 1 {
			add("element <%s> appears more than once", kid.name)
		}
		kp := path + "/" + kid.name
		if p.sc.typ == "array" {
			items := s.flatten(p.sc.items)
			for _, item := range kid.kids {
				if items.xmlName != "" && item.name != items.xmlName {
					*errs = append(*errs, fmt.Sprintf("%s: item is <%s>, want <%s>", kp, item.name, items.xmlName))
					continue
				}
				s.node(item, items, kp+"/"+item.name, errs)
			}
			continue
		}
		s.node(kid, p.sc, kp, errs)
	}
	for k := range sc.required {
		raw := sc.props[k]
		if raw == nil {
			continue
		}
		ps := s.flatten(raw)
		if ps.xmlAttr {
			continue
		}
		if name := orStr(ps.xmlName, k); seen[name] == 0 {
			add("missing element <%s>", name)
		}
	}
}

func (s *spec) primitive(v string, sc sch, path string, errs *[]string) {
	add := func(format string, args ...any) { *errs = append(*errs, path+": "+fmt.Sprintf(format, args...)) }
	if sc.enum != nil {
		found := false
		for _, e := range sc.enum {
			found = found || fmt.Sprint(e) == v
		}
		if !found {
			add("%q is not one of %v", v, sc.enum)
		}
	}
	switch sc.typ {
	case "string", "":
		if sc.pattern != "" {
			if re, err := regexp.Compile(sc.pattern); err == nil && !re.MatchString(v) {
				add("%q does not match %s", v, sc.pattern)
			}
		}
		if n := utf8.RuneCountInString(v); sc.maxLength >= 0 && n > sc.maxLength {
			add("%d characters, more than %d", n, sc.maxLength)
		}
		if n := utf8.RuneCountInString(v); sc.minLength >= 0 && n < sc.minLength {
			add("%d characters, fewer than %d", n, sc.minLength)
		}
		switch sc.format {
		case "date-time":
			if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
				add("%q is not a date-time", v)
			}
		case "uri":
			if u, err := url.Parse(v); err != nil || u.Scheme == "" {
				add("%q is not a URI", v)
			}
		}
	case "integer":
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			add("%q is not an integer", v)
		}
	case "number":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			add("%q is not a number", v)
		}
	case "boolean":
		if v != "true" && v != "false" {
			add("%q is not a boolean", v)
		}
	}
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- The validator's own tests ---

func tiny(t *testing.T, schemas string) *spec {
	t.Helper()
	var schemasMap map[string]any
	if err := json.Unmarshal([]byte(schemas), &schemasMap); err != nil {
		t.Fatal(err)
	}
	return &spec{doc: map[string]any{"components": map[string]any{"schemas": schemasMap}}}
}

func TestValidatorErrors(t *testing.T) {
	s := tiny(t, `{
	  "Thing": {"type":"object","required":["Name","Kind"],"properties":{
	    "Name":{"type":"string","maxLength":5,"pattern":"^[a-z]+$"},
	    "Kind":{"$ref":"#/components/schemas/Kind"},
	    "Count":{"type":"integer"},
	    "When":{"type":"string","format":"date-time"},
	    "Tags":{"type":"array","items":{"$ref":"#/components/schemas/Tag"}},
	    "Id":{"type":"string","xml":{"attribute":true}}}},
	  "Kind": {"type":"string","enum":["A","B"]},
	  "Tag": {"type":"string","xml":{"name":"tag"}}}`)
	ref := map[string]any{"$ref": "#/components/schemas/Thing"}

	good := `<Thing Id="x"><Name>abc</Name><Kind>A</Kind><Count>3</Count><When>2026-01-01T10:00:00.000Z</When><Tags><tag>t</tag></Tags><Signature/></Thing>`
	if errs := s.validate([]byte(good), ref, "Thing"); len(errs) != 0 {
		t.Fatalf("a valid document has errors: %v", errs)
	}

	bad := map[string]string{
		`<Thing><Name>abc</Name></Thing>`:                                            "missing element <Kind>",
		`<Thing><Name>abcdefg</Name><Kind>A</Kind></Thing>`:                          "more than 5",
		`<Thing><Name>ABC</Name><Kind>A</Kind></Thing>`:                              "does not match",
		`<Thing><Name>abc</Name><Kind>Z</Kind></Thing>`:                              "is not one of",
		`<Thing><Name>abc</Name><Kind>A</Kind><Count>x</Count></Thing>`:              "not an integer",
		`<Thing><Name>abc</Name><Kind>A</Kind><When>yesterday</When></Thing>`:        "not a date-time",
		`<Thing><Name>abc</Name><Kind>A</Kind><Extra/></Thing>`:                      "unexpected element <Extra>",
		`<Thing><Name>abc</Name><Kind>A</Kind><Tags><wrong>t</wrong></Tags></Thing>`: "want <tag>",
		`<Thing><Name>abc</Name><Name>abd</Name><Kind>A</Kind></Thing>`:              "more than once",
		`<Other><Name>abc</Name><Kind>A</Kind></Other>`:                              "root element is <Other>",
		`not xml`: "no root element",
	}
	for doc, want := range bad {
		errs := s.validate([]byte(doc), ref, "Thing")
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), want) {
			t.Errorf("%s\n  errors %v; want one containing %q", doc, errs, want)
		}
	}
}

func TestValidatorComposition(t *testing.T) {
	s := tiny(t, `{
	  "Key": {"type":"string","maxLength":5},
	  "ExtKey": {"allOf":[{"$ref":"#/components/schemas/Key"},{"type":"string","xml":{"name":"Key"},"properties":{"hasEntry":{"type":"boolean","xml":{"attribute":true}}},"required":["hasEntry"]}]},
	  "Box": {"type":"object","properties":{"Keys":{"type":"array","items":{"$ref":"#/components/schemas/ExtKey"}},
	     "Who":{"$ref":"#/components/schemas/Who"}}},
	  "Who": {"type":"object","oneOf":[
	     {"type":"object","properties":{"Kind":{"type":"string","enum":["P"]},"Cpf":{"type":"string","pattern":"^[0-9]{3}$"}},"required":["Kind","Cpf"]},
	     {"type":"object","properties":{"Kind":{"type":"string","enum":["L"]},"Cnpj":{"type":"string","pattern":"^[0-9]{4}$"}},"required":["Kind","Cnpj"]}]}}`)
	ref := map[string]any{"$ref": "#/components/schemas/Box"}

	if errs := s.validate([]byte(`<Box><Keys><Key hasEntry="true">abc</Key><Key hasEntry="false">d</Key></Keys><Who><Kind>L</Kind><Cnpj>1234</Cnpj></Who></Box>`), ref, "Box"); len(errs) != 0 {
		t.Fatalf("valid document: %v", errs)
	}
	for doc, want := range map[string]string{
		`<Box><Keys><Key>abc</Key></Keys></Box>`:                     "missing attribute hasEntry",
		`<Box><Keys><Key hasEntry="maybe">abc</Key></Keys></Box>`:    "not a boolean",
		`<Box><Keys><Key hasEntry="true">abcdefg</Key></Keys></Box>`: "more than 5",
		`<Box><Who><Kind>P</Kind><Cnpj>1234</Cnpj></Who></Box>`:      "none of the 2 alternatives",
		`<Box><Who><Kind>L</Kind><Cnpj>12</Cnpj></Who></Box>`:        "none of the 2 alternatives",
	} {
		errs := s.validate([]byte(doc), ref, "Box")
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), want) {
			t.Errorf("%s\n  errors %v; want one containing %q", doc, errs, want)
		}
	}
}
