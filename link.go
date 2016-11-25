package goaeoas

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sync/atomic"
)

type LinkDecorator func(*Link, *url.URL) error

type Links []Link

func (l Links) Len() int {
	return len(l)
}

func (l Links) Less(i, j int) bool {
	if l[i].Method == l[j].Method {
		return l[i].Rel < l[j].Rel
	}
	iGet := l[i].Method == "GET" || l[i].Method == ""
	jGet := l[j].Method == "GET" || l[j].Method == ""
	return iGet && !jGet
}

func (l Links) Swap(i, j int) {
	l[i], l[j] = l[j], l[i]
}

type Link struct {
	baseScheme     string
	baseHost       string
	linkDecorators []LinkDecorator

	Rel         string
	Route       string
	RouteParams []string
	QueryParams url.Values
	Method      string
	Type        reflect.Type
}

func (l *Link) Resolve() (string, error) {
	u, err := router.Get(l.Route).URL(l.RouteParams...)
	if err != nil {
		return "", err
	}
	u.RawQuery = l.QueryParams.Encode()
	u.Scheme = l.baseScheme
	u.Host = l.baseHost
	for _, decorator := range l.linkDecorators {
		if err := decorator(l, u); err != nil {
			return "", err
		}
	}
	return u.String(), nil
}

func (l *Link) HTMLNode() (*Node, error) {
	if l.Method == "" {
		l.Method = "GET"
	}
	u, err := l.Resolve()
	if err != nil {
		return nil, err
	}
	if l.Method == "GET" {
		linkNode := NewEl("a", "href", u)
		linkNode.AddText(l.Rel)
		return linkNode, nil
	}
	var docType *DocType
	if l.Type != nil {
		docType, err = NewDocType(l.Type, l.Method)
		if err != nil {
			return nil, err
		}
	}
	if (l.Method == "POST" || l.Method == "PUT") && docType != nil && len(docType.Fields) > 0 {
		linkNode := NewEl("div")
		formID := fmt.Sprintf("form%d", atomic.AddUint64(&nextElementID, 1))
		linkNode.AddEl("form", "id", formID)
		schema, err := docType.ToJSONSchema()
		if err != nil {
			return nil, err
		}
		schemaJSON, err := json.MarshalIndent(schema, "  ", "  ")
		if err != nil {
			return nil, err
		}
		linkNode.AddEl("script").AddText(fmt.Sprintf(`
$('#%s').jsonForm({
  schema: %s,
	form: [
	  "*",
		{
			"type": "submit",
			"title": %q
		}
	],
	onSubmitValid: function(values) {
		var req = new XMLHttpRequest();
		req.addEventListener("readystatechange", function(ev) {
			if (req.readyState == 4) {
				if (req.status > 199 && req.status < 300) {
					alert("done");
				} else {
					alert(req.responseText);
				}
			}
		});
		req.open(%q, %q);
		req.setRequestHeader("Content-Type", "application/json; charset=utf-8");
		req.send(JSON.stringify(values));
		return false;
	}
});
`, formID, schemaJSON, l.Rel, l.Method, u))
		return linkNode, nil
	}
	linkNode := NewEl("div")
	buttonID := atomic.AddUint64(&nextElementID, 1)
	linkNode.AddEl("button", "id", fmt.Sprintf("button%d", buttonID)).AddText(l.Rel)
	linkNode.AddEl("script").AddText(fmt.Sprintf(`
document.getElementById("button%d").addEventListener("click", function(ev) {
	var req = new XMLHttpRequest();
	req.addEventListener("readystatechange", function(ev) {
		if (req.readyState == 4) {
			if (req.status > 199 && req.status < 300) {
				alert("done");
			} else {
				alert(req.responseText);
			}
		}
	});
	req.open(%q, %q);
  req.send();
});
`, buttonID, l.Method, u))
	return linkNode, nil
}

func (l Link) MarshalJSON() ([]byte, error) {
	u, err := l.Resolve()
	if err != nil {
		return nil, err
	}
	method := l.Method
	if method == "" {
		method = "GET"
	}
	generated := struct {
		Rel        string
		URL        string
		Method     string
		JSONSchema *JSONSchema `json:",omitempty"`
	}{
		Rel:    l.Rel,
		URL:    u,
		Method: method,
	}
	if (l.Method == "POST" || l.Method == "PUT") && l.Type != nil {
		docType, err := NewDocType(l.Type, l.Method)
		if err != nil {
			return nil, err
		}
		schema, err := docType.ToJSONSchema()
		if err != nil {
			return nil, err
		}
		generated.JSONSchema = schema
	}
	return json.Marshal(generated)
}
