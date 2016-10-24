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

	"github.com/gorilla/mux"
)

var (
	router  *mux.Router
	filters []func(ResponseWriter, Request) error
)

var (
	responseWriterType = reflect.TypeOf((*ResponseWriter)(nil)).Elem()
	requestType        = reflect.TypeOf((*Request)(nil)).Elem()
	itemerType         = reflect.TypeOf((*Itemer)(nil)).Elem()
	errorType          = reflect.TypeOf((*error)(nil)).Elem()
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

type Link struct {
	baseScheme     string
	baseHost       string
	linkDecorators []LinkDecorator

	Rel         string
	Route       string
	RouteParams []string
	QueryParams url.Values
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

func (l Link) MarshalJSON() ([]byte, error) {
	u, err := l.Resolve()
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Rel string
		URL string
	}{
		Rel: l.Rel,
		URL: u,
	})
}

type List []Content

type Item struct {
	Properties interface{}
	Name       string
	Desc       [][]string
	Links      []Link
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
		Properties interface{}
		Desc       [][]string
		Type       string
		Links      []Link
	}{
		Properties: i.Properties,
		Desc:       i.Desc,
		Type:       propertyValue.Type().Name(),
		Links:      i.Links,
	})
}

func (i Item) HTMLNode() (*Node, error) {
	selfLink := ""
	restLinks := []Link{}
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

	itemNode := NewEl("section")
	titleNode := itemNode.AddEl("header")
	if selfLink == "" {
		titleNode.AddText(i.Name)
	} else {
		titleNode.AddEl("a", "href", selfLink).AddText(i.Name)
	}
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
			u, err := link.Resolve()
			if err != nil {
				return nil, err
			}
			navNode.AddEl("a", "href", u).AddText(link.Rel)
		}
	}
	return itemNode, nil
}

type Resource struct {
	Create interface{}
	Update interface{}
	Delete interface{}
	Load   interface{}

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

func (r *Resource) Link(rel string, meth Method, id interface{}) Link {
	if meth == Create {
		return Link{
			Rel:   rel,
			Route: r.Route(meth),
		}
	} else {
		return Link{
			Rel:         rel,
			Route:       r.Route(meth),
			RouteParams: []string{"id", fmt.Sprint(id)},
		}
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
	pattern := ""
	if meth == Create {
		pattern = fmt.Sprintf("/%s", rType.Name())
	} else {
		pattern = fmt.Sprintf("/%s/{id}", rType.Name())
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

func AddFilter(f func(ResponseWriter, Request) error) {
	filters = append(filters, f)
}

func Handle(ro *mux.Router, pattern string, methods []string, routeName string, f func(ResponseWriter, Request) error) {
	if router == nil {
		router = ro
	} else if router != ro {
		panic("only one *mux.Router allowed")
	}
	log.Printf("%v\t%+v\t%v", pattern, methods, routeName)
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
		if params["charset"] != "utf-8" {
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
			if err := filter(w, r); err != nil {
				http.Error(httpW, err.Error(), 500)
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
