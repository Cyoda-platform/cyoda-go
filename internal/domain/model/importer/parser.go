package importer

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseJSON parses a JSON reader into a generic any tree.
// Numbers are preserved as json.Number to maintain precision for large
// integers and big decimals.
func ParseJSON(r io.Reader) (any, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("json parse: %w", err)
	}
	return raw, nil
}

// ParseXML parses an XML reader into a generic any tree (map[string]any).
// Repeated elements become []any. Attributes become fields.
func ParseXML(r io.Reader) (any, error) {
	dec := xml.NewDecoder(r)
	var root map[string]any
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml parse: %w", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			child, err := parseXMLElement(dec, se)
			if err != nil {
				return nil, err
			}
			if m, ok := child.(map[string]any); ok {
				root = m
			} else {
				root = map[string]any{"_value": child}
			}
			break
		}
	}
	if root == nil {
		return nil, fmt.Errorf("xml parse: empty document")
	}
	return root, nil
}

func parseXMLElement(dec *xml.Decoder, start xml.StartElement) (any, error) {
	fields := make(map[string]any)
	for _, attr := range start.Attr {
		fields[attr.Name.Local] = inferXMLValue(attr.Value)
	}
	var textParts []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("xml parse element %s: %w", start.Name.Local, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := parseXMLElement(dec, t)
			if err != nil {
				return nil, err
			}
			name := t.Name.Local
			if existing, ok := fields[name]; ok {
				if arr, isArr := existing.([]any); isArr {
					fields[name] = append(arr, child)
				} else {
					fields[name] = []any{existing, child}
				}
			} else {
				fields[name] = child
			}
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" {
				textParts = append(textParts, s)
			}
		case xml.EndElement:
			if len(fields) == 0 && len(textParts) > 0 {
				return inferXMLValue(strings.Join(textParts, " ")), nil
			}
			if len(textParts) > 0 {
				fields["_text"] = inferXMLValue(strings.Join(textParts, " "))
			}
			return fields, nil
		}
	}
}

func inferXMLValue(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(i)
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	return s
}
