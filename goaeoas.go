package goaeoas

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
)

var (
	router        *mux.Router
	filters       []func(ResponseWriter, Request) (bool, error)
	postProcs     []func(ResponseWriter, Request, error) (bool, error)
	schemaDecoder = schema.NewDecoder()
	nextElementID uint64
	headCallbacks []func(*Node) error
	jsonFormURL   *url.URL
	jsvURL        *url.URL
)

const (
	DateTimeInputFormat = "2006-01-02T15:04"
)

var (
	responseWriterType = reflect.TypeOf((*ResponseWriter)(nil)).Elem()
	requestType        = reflect.TypeOf((*Request)(nil)).Elem()
	itemerType         = reflect.TypeOf((*Itemer)(nil)).Elem()
	errorType          = reflect.TypeOf((*error)(nil)).Elem()
	keyType            = reflect.TypeOf(&datastore.Key{})
	timeType           = reflect.TypeOf(time.Now())
)

type HTTPErr struct {
	Body   string
	Status int
}

func (h HTTPErr) Error() string {
	return fmt.Sprintf("%s: %d", h.Body, h.Status)
}

func HandleError(w http.ResponseWriter, err error) {
	if herr, ok := err.(HTTPErr); ok {
		http.Error(w, herr.Body, herr.Status)
		return
	}

	if err == datastore.ErrNoSuchEntity {
		http.Error(w, err.Error(), 404)
		return
	}

	if merr, ok := err.(appengine.MultiError); ok {
		only404 := true
		for _, err := range merr {
			if err != nil && err != datastore.ErrNoSuchEntity {
				only404 = false
				break
			}
		}
		if only404 {
			http.Error(w, err.Error(), 404)
			return
		}
	}

	http.Error(w, err.Error(), 500)
}

type Method int

const (
	Create Method = iota
	Update
	Delete
	Load
)

func (m Method) String() string {
	switch m {
	case Create:
		return "Create"
	case Update:
		return "Update"
	case Delete:
		return "Delete"
	case Load:
		return "Load"
	}
	return "Unknown"
}

func (m Method) HTTPMethod() string {
	switch m {
	case Create:
		return "POST"
	case Update:
		return "PUT"
	case Delete:
		return "DELETE"
	case Load:
		return "GET"
	}
	return "UNKNOWN"
}

type Request interface {
	Req() *http.Request
	Vars() map[string]string
	NewLink(Link) Link
	Values() map[string]interface{}
	DecorateLinks(LinkDecorator)
	Media() string
}

type request struct {
	req            *http.Request
	vars           map[string]string
	values         map[string]interface{}
	linkDecorators []LinkDecorator
	media          string
}

func (r *request) Media() string {
	return r.media
}

func (r *request) Values() map[string]interface{} {
	return r.values
}

func (r *request) DecorateLinks(f LinkDecorator) {
	r.linkDecorators = append(r.linkDecorators, f)
}

func (r *request) NewLink(l Link) Link {
	rval := l
	if r.Req().TLS == nil {
		rval.baseScheme = "http"
	} else {
		rval.baseScheme = "https"
	}
	rval.baseHost = r.Req().Host
	rval.linkDecorators = r.linkDecorators
	return rval
}

func (r *request) Req() *http.Request {
	return r.req
}

func (r *request) Vars() map[string]string {
	return r.vars
}

type ResponseWriter interface {
	http.ResponseWriter
	SetContent(Content)
}

type responseWriter struct {
	http.ResponseWriter
	content Content
}

func (r *responseWriter) SetContent(c Content) {
	r.content = c
}

type Content interface {
	HTMLNode() (*Node, error)
}

type Itemer interface {
	Item(Request) *Item
}

type Properties interface{}

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

func Copy(dest interface{}, r Request, method string) error {
	media, params, err := mime.ParseMediaType(r.Req().Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	if charset := params["charset"]; strings.ToLower(charset) != "utf-8" && charset != "" {
		return fmt.Errorf("unsupported character set %v", charset)
	}
	switch media {
	case "application/json":
		return copyJSON(dest, r.Req().Body, method)
	}
	return fmt.Errorf("unsupported Content-Type %v", media)
}

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

func (d DocType) ToJSONSchema() (*JSONSchema, error) {
	schemaType := &JSONSchema{}
	switch d.typ.Kind() {
	case reflect.Ptr:
		log.Printf("*** d.typ is %v, == %v => %v", d.typ, keyType, d.typ == keyType)
		if d.typ == keyType {
			schemaType.Type = "string"
		} else {
			return nil, fmt.Errorf("Untranslatable Go Kind %q", d.Kind)
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
	default:
		return nil, fmt.Errorf("Untranslatable Go Kind %q", d.Kind)
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

func filterJSON(typ reflect.Type, m map[string]interface{}, method string) error {
	docType, err := NewDocType(typ, method)
	if err != nil {
		return err
	}
	for key, value := range m {
		field, found := docType.GetField(key)
		if found {
			if len(field.Type.Fields) > 0 {
				if err := filterJSON(field.Type.typ, value.(map[string]interface{}), method); err != nil {
					return err
				}
			} else if field.Type.Elem != nil && len(field.Type.Elem.Fields) > 0 {
				for _, elem := range value.([]interface{}) {
					if err := filterJSON(field.Type.Elem.typ, elem.(map[string]interface{}), method); err != nil {
						return err
					}
				}
			}
		} else {
			delete(m, key)
		}
	}
	return nil
}

type List []Content

type Item struct {
	Properties interface{}
	Name       string
	Desc       [][]string
	Links      Links
}

func NewItem(i interface{}) *Item {
	return &Item{
		Properties: i,
	}
}

func (i *Item) SetDesc(s [][]string) *Item {
	i.Desc = s
	return i
}

func (i *Item) SetName(s string) *Item {
	i.Name = s
	return i
}

func (i *Item) AddLink(l Link) *Item {
	i.Links = append(i.Links, l)
	return i
}

func (i Item) MarshalJSON() ([]byte, error) {
	propertyValue := reflect.ValueOf(i.Properties)
	for propertyValue.Kind() == reflect.Ptr {
		propertyValue = propertyValue.Elem()
	}
	return json.Marshal(struct {
		Name       string
		Properties interface{}
		Desc       [][]string `json:",omitempty"`
		Type       string
		Links      Links
	}{
		Properties: i.Properties,
		Desc:       i.Desc,
		Type:       propertyValue.Type().Name(),
		Links:      i.Links,
		Name:       i.Name,
	})
}

func (i Item) HTMLNode() (*Node, error) {
	selfLink := ""
	restLinks := Links{}
	for _, link := range i.Links {
		if link.Rel == "self" {
			u, err := link.Resolve()
			if err != nil {
				return nil, err
			}
			selfLink = u
		} else {
			restLinks = append(restLinks, link)
		}
	}
	sort.Sort(restLinks)

	itemNode := NewEl("section")
	titleNode := itemNode.AddEl("header")
	if selfLink == "" {
		titleNode.AddText(i.Name)
	} else {
		titleNode.AddEl("a", "href", selfLink).AddText(i.Name)
	}
	if len(i.Desc) > 0 {
		descNode := itemNode.AddEl("section")
		descNode.AddEl("header").AddText("Description")
		for _, part := range i.Desc {
			if len(part) > 0 {
				articleNode := descNode.AddEl("article")
				articleNode.AddEl("header").AddText(part[0])
				for _, paragraph := range part[1:] {
					articleNode.AddEl("p").AddText(paragraph)
				}
			}
		}
	}
	propNode := itemNode.AddEl("section")
	propNode.AddEl("header").AddText("Properties")
	if list, ok := i.Properties.(List); ok {
		listNode := propNode.AddEl("ul")
		for _, item := range list {
			itemNode, err := item.HTMLNode()
			if err != nil {
				return nil, err
			}
			listNode.AddEl("ul").AddNode(itemNode)
		}
	} else {
		preNode := propNode.AddEl("article").AddEl("pre")
		pretty, err := json.MarshalIndent(i.Properties, "  ", "  ")
		if err != nil {
			return nil, err
		}
		preNode.AddText(string(pretty))
	}
	if len(restLinks) > 0 {
		navNode := itemNode.AddEl("nav")
		for _, link := range restLinks {
			linkNode, err := link.HTMLNode()
			if err != nil {
				return nil, err
			}
			navNode.AddNode(linkNode)
		}
	}
	return itemNode, nil
}

type Resource struct {
	Create     interface{}
	Update     interface{}
	Delete     interface{}
	Load       interface{}
	FullPath   string
	CreatePath string

	rType reflect.Type
}

func (r *Resource) resourceFunc(meth Method) interface{} {
	switch meth {
	case Create:
		return r.Create
	case Update:
		return r.Update
	case Delete:
		return r.Delete
	case Load:
		return r.Load
	}
	panic(fmt.Errorf("unknown method %s", meth))
}

func (r *Resource) Route(meth Method) string {
	return r.rType.Name() + "." + meth.String()
}

func (r *Resource) Link(rel string, meth Method, routeParams []string) Link {
	return Link{
		Rel:         rel,
		Route:       r.Route(meth),
		RouteParams: routeParams,
		Method:      meth.HTTPMethod(),
		Type:        r.rType,
	}
}

func (r *Resource) URL(meth Method, id interface{}) (*url.URL, error) {
	return router.Get(r.Route(meth)).URL("id", fmt.Sprint(id))
}

func validateResourceFunc(f interface{}, needType reflect.Type) (fVal reflect.Value, returnType reflect.Type) {
	fVal = reflect.ValueOf(f)
	fTyp := fVal.Type()
	if fTyp.Kind() != reflect.Func {
		panic(fmt.Errorf("%#v isn't a func", f))
	}
	if fTyp.NumIn() != 2 {
		panic(fmt.Errorf("%#v isn't a func with two params", f))
	}
	if !fTyp.In(0).Implements(responseWriterType) {
		panic(fmt.Errorf("%#v isn't a func with a ResponseWriter as its first param", f))
	}
	if !fTyp.In(1).Implements(requestType) {
		panic(fmt.Errorf("%#v isn't a func with a Request as its second param", f))
	}
	if fTyp.NumOut() != 2 {
		panic(fmt.Errorf("%#v isn't a func with two return values", f))
	}
	if !fTyp.Out(0).Implements(itemerType) {
		panic(fmt.Errorf("%#v isn't a func with Itemer as its first return value", f))
	}
	if !fTyp.Out(1).Implements(errorType) {
		panic(fmt.Errorf("%#v isn't a func with error as its second return value", f))
	}
	returnType = fTyp.Out(0)
	for returnType.Kind() == reflect.Ptr {
		returnType = returnType.Elem()
	}
	if needType != nil && needType != returnType {
		panic(fmt.Errorf("%#v and %#v not the same resource type", needType, returnType))
	}
	return fVal, returnType
}

func createRoute(ro *mux.Router, re *Resource, meth Method, rType reflect.Type) reflect.Type {
	var fVal reflect.Value
	fVal, rType = validateResourceFunc(re.resourceFunc(meth), rType)
	re.rType = rType
	if re.CreatePath == "" {
		re.CreatePath = fmt.Sprintf("/%s", rType.Name())
	}
	if re.FullPath == "" {
		re.FullPath = fmt.Sprintf("%s/{id}", re.CreatePath)
	}
	pattern := ""
	if meth == Create {
		pattern = re.CreatePath
	} else {
		pattern = re.FullPath
	}
	Handle(
		ro,
		pattern,
		[]string{
			meth.HTTPMethod(),
		},
		re.Route(meth),
		func(w ResponseWriter, r Request) error {
			resultVals := fVal.Call([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)})
			if !resultVals[1].IsNil() {
				return resultVals[1].Interface().(error)
			}
			if !resultVals[0].IsNil() {
				w.SetContent(resultVals[0].Interface().(Itemer).Item(r))
			}
			return nil
		},
	)
	return rType
}

func HandleResource(ro *mux.Router, re *Resource) {
	var rType reflect.Type
	if re.Create != nil {
		rType = createRoute(ro, re, Create, rType)
	}
	if re.Update != nil {
		rType = createRoute(ro, re, Update, rType)
	}
	if re.Delete != nil {
		rType = createRoute(ro, re, Delete, rType)
	}
	if re.Load != nil {
		createRoute(ro, re, Load, rType)
	}
}

func AddFilter(f func(ResponseWriter, Request) (bool, error)) {
	filters = append(filters, f)
}

func AddPostProc(f func(ResponseWriter, Request, error) (bool, error)) {
	postProcs = append(postProcs, f)
}

func HeadCallback(f func(*Node) error) {
	headCallbacks = append(headCallbacks, f)
}

func SetJSONFormURL(u *url.URL) {
	jsonFormURL = u
}

func SetJSVURL(u *url.URL) {
	jsvURL = u
}

func Handle(ro *mux.Router, pattern string, methods []string, routeName string, f func(ResponseWriter, Request) error) {
	if router == nil {
		router = ro
	} else if router != ro {
		panic("only one *mux.Router allowed")
	}
	ro.Path(pattern).Methods(methods...).HandlerFunc(func(httpW http.ResponseWriter, httpR *http.Request) {
		log.Printf("%v\t%v\t%v ->", httpR.Method, httpR.URL.String(), routeName)
		CORSHeaders(httpW)
		media, params, err := mime.ParseMediaType(httpR.Header.Get("Accept"))
		if err != nil || media == "" || media == "*/*" {
			media = "text/html"
			params = map[string]string{
				"charset": "utf-8",
			}
		}
		if paramAccept := httpR.URL.Query().Get("accept"); paramAccept != "" {
			media = paramAccept
		}
		if params["charset"] == "" {
			params["charset"] = "utf-8"
		}

		if !map[string]bool{
			"text/html":        true,
			"application/json": true,
		}[media] {
			http.Error(httpW, "only accepts text/hml or application/json requests", 406)
			return
		}
		if strings.ToLower(params["charset"]) != "utf-8" {
			http.Error(httpW, "only accepts utf-8 requests", 406)
			return
		}

		w := &responseWriter{
			ResponseWriter: httpW,
		}
		r := &request{
			req:    httpR,
			vars:   mux.Vars(httpR),
			values: map[string]interface{}{},
			media:  media,
		}

		for _, filter := range filters {
			cont, err := filter(w, r)
			if err != nil {
				HandleError(httpW, err)
				return
			}
			if !cont {
				return
			}
		}

		err = f(w, r)
		cont := false
		for _, postProc := range postProcs {
			cont, err = postProc(w, r, err)
			if !cont {
				break
			}
		}
		if err != nil {
			HandleError(httpW, err)
		}

		if w.content != nil {
			renderF := map[string]func(http.ResponseWriter) error{
				"text/html": func(httpW http.ResponseWriter) error {
					contentNode, err := w.content.HTMLNode()
					if err != nil {
						return err
					}
					htmlNode := NewEl("html")
					headNode := htmlNode.AddEl("head")
					for _, cb := range headCallbacks {
						if err := cb(headNode); err != nil {
							return err
						}
					}
					headNode.AddEl("script", "src", "https://ajax.googleapis.com/ajax/libs/jquery/3.1.1/jquery.min.js")
					headNode.AddEl("script", "src", "https://cdnjs.cloudflare.com/ajax/libs/underscore.js/1.6.0/underscore-min.js")
					if jsonFormURL != nil {
						headNode.AddEl("script", "src", jsonFormURL.String())
					} else {
						headNode.AddEl("script").AddText(jsonformJS())
					}
					if jsvURL != nil {
						headNode.AddEl("script", "src", jsvURL.String())
					} else {
						headNode.AddEl("script").AddText(jsvJS())
					}
					headNode.AddEl("style").AddText(`
nav > form {
	padding: 5pt;
	margin: 0pt;
	border-style: inset;
}
section {
	border-style: outset;
	padding: 5pt;
	margin: 5pt;
}
section > header {
	font-weight: bold;
}
section > article {
	border-style: inset;
	padding: 5pt;
	margin: 5pt;
}
section > article > header {
	font-weight: bold;
}
nav {
	padding: 5pt;
	margin: 5pt;
}
nav > a {
	margin: 5pt;
}
`)
					htmlNode.AddEl("body").AddNode(contentNode)
					httpW.Header().Set("Content-Type", "text/html; charset=UTF-8")
					return htmlNode.Render(httpW)
				},
				"application/json": func(httpW http.ResponseWriter) error {
					httpW.Header().Set("Content-Type", "application/json; charset=UTF-8")
					return json.NewEncoder(httpW).Encode(w.content)
				},
			}[media]
			if err := renderF(httpW); err != nil {
				HandleError(httpW, err)
			}
		}
	}).Name(routeName)
}

func CORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}
