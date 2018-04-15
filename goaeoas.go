package goaeoas

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
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
	durationType       = reflect.TypeOf(time.Duration(0))
)

type HTTPErr struct {
	Body   string
	Status int
}

func (h HTTPErr) Error() string {
	return fmt.Sprintf("%s: %d", h.Body, h.Status)
}

func HTTPError(w http.ResponseWriter, r *http.Request, err error) {
	media, _ := Media(r, "Accept")
	handleError(w, media, err)
}

func httpError(w http.ResponseWriter, media, body string, status int) {
	log.Printf("Returning %v; %v", status, body)
	if media == "application/json" {
		b, err := json.Marshal(body)
		if err != nil {
			http.Error(w, body, 500)
			return
		}
		http.Error(w, string(b), status)
		return
	}
	http.Error(w, body, status)
}

func HandleError(w http.ResponseWriter, r Request, err error) {
	handleError(w, r.Media(), err)
}

func handleError(w http.ResponseWriter, media string, err error) {
	if herr, ok := err.(HTTPErr); ok {
		httpError(w, media, herr.Body, herr.Status)
		return
	}

	if err == datastore.ErrNoSuchEntity {
		httpError(w, media, err.Error(), 404)
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
			httpError(w, media, err.Error(), 404)
			return
		}
	}

	httpError(w, media, err.Error(), 500)
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

func Copy(dest interface{}, r Request, method string) error {
	b, err := ioutil.ReadAll(r.Req().Body)
	if err != nil {
		return err
	}
	return CopyBytes(dest, r, b, method)
}

func CopyBytes(dest interface{}, r Request, b []byte, method string) error {
	media, charset := Media(r.Req(), "Content-Type")
	if strings.ToLower(charset) != "utf-8" && charset != "" {
		return fmt.Errorf("unsupported character set %v", charset)
	}
	switch media {
	case "application/json":
		return copyJSON(dest, b, method)
	}
	return fmt.Errorf("unsupported Content-Type %v", media)
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

// Filters are run before the request handlers, and any
// filter returning false or an error will stop the handler
// from running.
// Returned errors will get forwarded to the client.
func AddFilter(f func(ResponseWriter, Request) (bool, error)) {
	filters = append(filters, f)
}

// PostProcs are run in sequence after the request handler.
// The first proc receives the error returned by the handler,
// and all consecutive procs get the error returned by the
// previous proc.
// Any proc returning false will stop further procs from running.
// The final returned error will be forwarded to the client.
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

func Media(r *http.Request, header string) (media, charset string) {
	media, params, err := mime.ParseMediaType(r.Header.Get(header))
	if err != nil || media == "" || media == "*/*" {
		media = "text/html"
		params = map[string]string{
			"charset": "utf-8",
		}
	}
	if paramAccept := r.URL.Query().Get("accept"); paramAccept != "" {
		media = paramAccept
	}
	if params["charset"] == "" {
		params["charset"] = "utf-8"
	}

	return media, params["charset"]
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
		media, charset := Media(httpR, "Accept")

		if !map[string]bool{
			"text/html":        true,
			"application/json": true,
		}[media] {
			http.Error(httpW, "only accepts text/hml or application/json requests", 406)
			return
		}
		if strings.ToLower(charset) != "utf-8" {
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
				HandleError(httpW, r, err)
				return
			}
			if !cont {
				return
			}
		}

		err := f(w, r)
		cont := false
		for _, postProc := range postProcs {
			cont, err = postProc(w, r, err)
			if !cont {
				break
			}
		}
		if err != nil {
			HandleError(httpW, r, err)
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
				HandleError(httpW, r, err)
			}
		}
	}).Name(routeName)
}

func CORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, DELETE, CONNECT, OPTIONS, PATCH")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
}
