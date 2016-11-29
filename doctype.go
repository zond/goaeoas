package goaeoas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

type DocType struct {
	Kind   string
	Name   string
	Elem   *DocType
	Fields []DocField
	typ    reflect.Type
	method string
}

func (d DocType) GetField(n string) (*DocField, bool) {
	for _, field := range d.Fields {
		if field.Name == n {
			return &field, true
		}
	}
	return nil, false
}

type JSONSchema struct {
	Type                 string                `json:"type"`
	Properties           map[string]JSONSchema `json:"properties,omitempty"`
	AdditionalProperties *JSONSchema           `json:"additionalProperties,omitempty"`
	Items                *JSONSchema           `json:"items,omitempty"`
	Title                string                `json:"title,omitempty"`
}

func (d DocType) ToJavaClasses(pkg, meth string) (map[string]string, error) {
	javaClasses := map[string]string{}
	if err := d.populateJavaClasses(javaClasses, pkg, meth); err != nil {
		return nil, err
	}
	return javaClasses, nil
}

func (d DocType) populateJavaClasses(javaClasses map[string]string, pkg, meth string) error {
	if _, found := javaClasses[d.typ.Name()]; found {
		return nil
	}

	buf := &bytes.Buffer{}
	fmt.Fprintf(buf, `package %s;

import retrofit2.http.*;
	
public class %s {
`, pkg, d.typ.Name())

	for _, field := range d.Fields {
		javaType, err := d.javaTypeFor(javaClasses, field.field.Type, pkg, meth)
		if err != nil {
			return err
		}
		fmt.Fprintf(buf, `  public %s %s;
`, javaType, field.Name)
	}

	fmt.Fprintf(buf, "}")
	javaClasses[d.typ.Name()] = buf.String()

	javaClasses[fmt.Sprintf("%sContainer", d.typ.Name())] = fmt.Sprintf(`package %s;
	
import retrofit2.http.*;
	
public class %sContainer {
  public %s Properties;
  public java.util.List<Link> Links;
  public String name;
  public java.util.List<java.util.List<String>> Desc;
}`, pkg, d.typ.Name(), d.typ.Name())

	javaClasses[fmt.Sprintf("%ssContainer", d.typ.Name())] = fmt.Sprintf(`package %s;
	
import retrofit2.http.*;
	
public class %ssContainer {
  public java.util.List<%sContainer> Properties;
  public java.util.List<Link> Links;
  public String name;
  public java.util.List<java.util.List<String>> Desc;
}`, pkg, d.typ.Name(), d.typ.Name())

	if _, found := javaClasses["Link"]; !found {
		javaClasses["Link"] = fmt.Sprintf(`package %s;
		
public class Link {
  public String Rel;
  public String URL;
  public String Method;
}`, pkg)
	}

	return nil
}

func (d DocType) javaTypeFor(javaClasses map[string]string, t reflect.Type, pkg, meth string) (string, error) {
	switch t.Kind() {
	case reflect.Ptr:
		if t == keyType {
			return "String", nil
		} else {
			return "", fmt.Errorf("Untranslatable Go Type %v", t)
		}
	case reflect.Map:
		javaKey, err := d.javaTypeFor(javaClasses, t.Key(), pkg, meth)
		if err != nil {
			return "", err
		}
		javaVal, err := d.javaTypeFor(javaClasses, t.Elem(), pkg, meth)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Map<%s,%s>", javaKey, javaVal), nil
	case reflect.Bool:
		return "Boolean", nil
	case reflect.String:
		return "String", nil
	case reflect.Struct:
		if t == timeType {
			return "java.util.Date", nil
		}
		dt, err := NewDocType(t, meth)
		if err != nil {
			return "", err
		}
		if err := dt.populateJavaClasses(javaClasses, pkg, meth); err != nil {
			return "", err
		}
		return t.Name(), nil
	case reflect.Slice:
		javaElem, err := d.javaTypeFor(javaClasses, t.Elem(), pkg, meth)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("java.util.List<%s>", javaElem), nil
	case reflect.Int64:
		fallthrough
	case reflect.Int32:
		fallthrough
	case reflect.Int:
		return "Long", nil
	case reflect.Float64:
		return "Double", nil
	}
	return "", fmt.Errorf("Untranslatable Go Type %v", t)
}

func (d DocType) ToJSONSchema() (*JSONSchema, error) {
	schemaType := &JSONSchema{}
	switch d.typ.Kind() {
	case reflect.Ptr:
		if d.typ == keyType {
			schemaType.Type = "string"
		} else {
			return nil, fmt.Errorf("Untranslatable Go Type %v", d.typ)
		}
	case reflect.Map:
		schemaType.Type = "object"
		valueDocType, err := NewDocType(d.typ.Elem(), d.method)
		if err != nil {
			return nil, err
		}
		valueType, err := valueDocType.ToJSONSchema()
		if err != nil {
			return nil, err
		}
		schemaType.AdditionalProperties = valueType
	case reflect.Bool:
		schemaType.Type = "boolean"
	case reflect.String:
		schemaType.Type = "string"
	case reflect.Struct:
		switch d.typ {
		case timeType:
			schemaType.Type = "datetime"
		default:
			schemaType.Type = "object"
			schemaType.Properties = map[string]JSONSchema{}
			for _, field := range d.Fields {
				s, err := field.ToJSONSchema()
				if err != nil {
					return nil, err
				}
				schemaType.Properties[field.Name] = *s
			}
		}
	case reflect.Slice:
		schemaType.Type = "array"
		elType, err := d.Elem.ToJSONSchema()
		if err != nil {
			return nil, err
		}
		schemaType.Items = elType
	case reflect.Int64:
		fallthrough
	case reflect.Int32:
		fallthrough
	case reflect.Int:
		schemaType.Type = "integer"
	case reflect.Float64:
		schemaType.Type = "number"
	default:
		return nil, fmt.Errorf("Untranslatable Go Type %v", d.typ)
	}
	return schemaType, nil
}

type DocField struct {
	Name  string
	Type  *DocType
	field reflect.StructField
}

func (d DocField) ToJSONSchema() (*JSONSchema, error) {
	typ, err := d.Type.ToJSONSchema()
	if err != nil {
		return nil, err
	}
	typ.Title = d.Name
	return typ, nil
}

func NewDocFields(typ reflect.Type, method string) ([]DocField, error) {
	result := []DocField{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		found := false
		if method == "GET" || method == "" {
			found = field.Tag.Get("json") != "-"
		} else {
			methods := strings.Split(field.Tag.Get("methods"), ",")
			for j := 0; j < len(methods); j++ {
				if methods[j] == method {
					found = true
					break
				}
			}
		}
		if found {
			if field.Anonymous {
				f, err := NewDocFields(field.Type, method)
				if err != nil {
					return nil, err
				}
				result = append(result, f...)
			} else {
				d, err := NewDocType(field.Type, method)
				if err != nil {
					return nil, err
				}
				result = append(result, DocField{
					Name:  field.Name,
					Type:  d,
					field: field,
				})
			}
		}
	}
	return result, nil
}

func NewDocType(typ reflect.Type, method string) (*DocType, error) {
	result := &DocType{
		Name:   typ.String(),
		Kind:   typ.Kind().String(),
		typ:    typ,
		method: method,
	}
	switch typ.Kind() {
	case reflect.Struct:
		var err error
		result.Fields, err = NewDocFields(typ, method)
		if err != nil {
			return nil, err
		}
	case reflect.Slice:
		elem, err := NewDocType(typ.Elem(), method)
		if err != nil {
			return nil, err
		}
		result.Elem = elem
	}
	return result, nil
}

func copyJSON(dest interface{}, r io.Reader, method string) error {
	decoded := map[string]interface{}{}
	if err := json.NewDecoder(r).Decode(&decoded); err != nil {
		return err
	}
	val := reflect.ValueOf(dest)
	if val.Kind() != reflect.Ptr {
		return fmt.Errorf("can only copy to pointer to struct")
	}
	val = val.Elem()
	if val.Kind() != reflect.Struct {
		return fmt.Errorf("can only copy to pointer to struct")
	}
	typ := val.Type()
	if err := filterJSON(typ, decoded, method); err != nil {
		return err
	}
	filtered, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	return json.Unmarshal(filtered, dest)
}
