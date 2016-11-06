package goaeoas

import (
	"encoding/json"
	"fmt"
	"io"
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
	"google.golang.org/appengine/datastore"
)

var (
	router        *mux.Router
	filters       []func(ResponseWriter, Request) (bool, error)
	schemaDecoder = schema.NewDecoder()
	nextElementID uint64
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
	objID := atomic.AddUint64(&nextElementID, 1)
	linkNode := NewEl("div")
	sendCode := `
  req.send();
`
	if (l.Method == "POST" || l.Method == "PUT") && l.Type != nil {
		linkNode.AddEl("script").AddText(fmt.Sprintf(`
var obj%dHooks = [];
		`, objID))
		tableNode := linkNode.AddEl("table")
		fields, err := validFields("", l.Type, l.Method)
		if err != nil {
			return nil, err
		}
		for path, field := range fields {
			rowNode := tableNode.AddEl("tr")
			rowNode.AddEl("td").AddText(path)
			switch field.field.Type.Kind() {
			case reflect.Slice:
				separator := field.field.Tag.Get("separator")
				if separator == "" {
					separator = ","
				}
				switch field.field.Type.Elem().Kind() {
				case reflect.String:
					elID := atomic.AddUint64(&nextElementID, 1)
					rowNode.AddEl("td").AddEl("input", "type", "text", "name", path, "id", fmt.Sprintf("input%d", elID), "placeholder", fmt.Sprintf("separated by %q", separator))
					rowNode.AddEl("script").AddText(fmt.Sprintf(`
obj%dHooks.push(function(obj) {
	obj[%q] = document.getElementById("input%d").value.split(%q);
});
`, objID, path, elID, separator))
				}
			case reflect.Int:
				elID := atomic.AddUint64(&nextElementID, 1)
				rowNode.AddEl("td").AddEl("input", "type", "number", "step", "1", "name", path, "id", fmt.Sprintf("input%d", elID))
				rowNode.AddEl("script").AddText(fmt.Sprintf(`
obj%dHooks.push(function(obj) {
	obj[%q] = parseInt(document.getElementById("input%d").value);
});
`, objID, path, elID))
			case reflect.Bool:
				elID := atomic.AddUint64(&nextElementID, 1)
				rowNode.AddEl("td").AddEl("input", "type", "checkbox", "name", path, "id", fmt.Sprintf("input%d", elID))
				rowNode.AddEl("script").AddText(fmt.Sprintf(`
obj%dHooks.push(function(obj) {
	obj[%q] = document.getElementById("input%d").checked;
});
`, objID, path, elID))
			case reflect.String:
				elID := atomic.AddUint64(&nextElementID, 1)
				rowNode.AddEl("td").AddEl("input", "type", "text", "name", path, "id", fmt.Sprintf("input%d", elID))
				rowNode.AddEl("script").AddText(fmt.Sprintf(`
obj%dHooks.push(function(obj) {
	obj[%q] = document.getElementById("input%d").value;
});
`, objID, path, elID))
			}
		}
		sendCode = fmt.Sprintf(`
  req.setRequestHeader("Content-Type", "application/json; charset=utf-8");
  var obj = {};
  for (var i = 0; i < obj%dHooks.length; i++) {
  	obj%dHooks[i](obj);
  }
	req.send(JSON.stringify(obj));
`, objID, objID)
	}
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
%s
});
`, buttonID, l.Method, u, sendCode))
	return linkNode, nil
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

type validField struct {
	field  reflect.StructField
	prefix string
}

func validFields(prefix string, typ reflect.Type, method string) (map[string]validField, error) {
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("validFields only works on structs")
	}
	result := map[string]validField{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		methods := strings.Split(field.Tag.Get("methods"), ",")
		found := false
		for j := 0; j < len(methods); j++ {
			if methods[j] == method {
				found = true
				break
			}
		}
		if found {
			if field.Type.Kind() == reflect.Struct {
				var subFields map[string]validField
				var err error
				if field.Anonymous {
					subFields, err = validFields("", field.Type, method)
				} else {
					subFields, err = validFields(fmt.Sprintf("%s.", field.Name), field.Type, method)
				}
				if err != nil {
					return nil, err
				}
				for path, subField := range subFields {
					result[path] = subField
				}
			} else {
				result[fmt.Sprintf("%s%s", prefix, field.Name)] = validField{
					field:  field,
					prefix: prefix,
				}
			}
		}
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
	fields, err := validFields("", typ, method)
	if err != nil {
		return err
	}
	for key := range decoded {
		if _, found := fields[key]; !found {
			delete(decoded, key)
		}
	}
	filtered, err := json.Marshal(decoded)
	if err != nil {
		return err
	}
	return json.Unmarshal(filtered, dest)
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
		Rel    string
		URL    string
		Method string
		Type   map[string]interface{} `json:",omitempty"`
	}{
		Rel:    l.Rel,
		URL:    u,
		Method: method,
	}
	if l.Type != nil {
		typ := map[string]interface{}{}
		for i := 0; i < l.Type.NumField(); i++ {
			field := l.Type.Field(i)
			methods := strings.Split(field.Tag.Get("methods"), ",")
			found := false
			for j := 0; j < len(methods); j++ {
				if methods[j] == method {
					found = true
					break
				}
			}
			if found {
				typ[field.Name] = field.Type.Name()
			}
		}
		generated.Type = typ
	}
	return json.Marshal(generated)
}

type List []Content

type Item struct {
	Properties interface{}
	Name       string
	Desc       [][]string `json:",omitempty"`
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
		Desc       [][]string
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

func Handle(ro *mux.Router, pattern string, methods []string, routeName string, f func(ResponseWriter, Request) error) {
	if router == nil {
		router = ro
	} else if router != ro {
		panic("only one *mux.Router allowed")
	}
	ro.Path(pattern).Methods(methods...).HandlerFunc(func(httpW http.ResponseWriter, httpR *http.Request) {
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
				http.Error(httpW, err.Error(), 500)
				return
			}
			if !cont {
				return
			}
		}

		if err := f(w, r); err != nil {
			http.Error(httpW, err.Error(), 500)
			return
		}

		if w.content != nil {
			renderF := map[string]func(io.Writer) error{
				"text/html": func(httpW io.Writer) error {
					contentNode, err := w.content.HTMLNode()
					if err != nil {
						return err
					}
					htmlNode := NewEl("html")
					htmlNode.AddEl("head").AddEl("style").AddText(`
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
					return htmlNode.Render(httpW)
				},
				"application/json": func(httpW io.Writer) error {
					return json.NewEncoder(httpW).Encode(w.content)
				},
			}[media]
			if err := renderF(httpW); err != nil {
				http.Error(httpW, err.Error(), 500)
			}
		}
	}).Name(routeName)
}

func CORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}
